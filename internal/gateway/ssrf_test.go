package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The M4-T8 adversarial SSRF suite (ROADMAP M4-T8, ARCHITECTURE
// §31.3). The T5 policy corpus (policy_test.go) pins the guard's
// happy-path rows; this suite attacks it the way an attacker would:
// full range coverage with boundary IPs, integer-host IP
// obfuscation, DNS rebinding across redirect hops, and the
// production-defaults regression ADR-0017 §4 promised. The
// acceptance criterion — "all attacks fail closed; events logged
// under airlock/network categories" — is asserted per test.

// TestSSRF_NonRoutableCIDRBoundaries walks every CIDR in the
// §21.5 deny-list plus boundary IPs (network address, middle, last
// host). This is the list↔test sync guarantee: a future edit that
// removes a range from cidrs.go fails here, and a typo in a range's
// mask shows up as a boundary IP slipping through.
func TestSSRF_NonRoutableCIDRBoundaries(t *testing.T) {
	for _, cidr := range nonRoutableCIDRs {
		name := cidr.String()
		t.Run(name, func(t *testing.T) {
			base := cidr.IP
			ones, bits := cidr.Mask.Size()
			hostBits := bits - ones
			samples := []net.IP{dupIP(base)}
			if hostBits > 0 {
				mid := dupIP(base)
				incIP(mid, uint64(1)<<(hostBits-1))
				samples = append(samples, mid)
				last := dupIP(base)
				incIP(last, (uint64(1)<<hostBits)-1)
				samples = append(samples, last)
			}
			for _, ip := range samples {
				if !isNonRoutable(ip) {
					t.Errorf("isNonRoutable(%s) = false inside %s; the range is not covered", ip, name)
				}
			}
		})
	}
	// Positive controls: public IPs must stay routable, or every
	// fetch would be denied.
	for _, pub := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700::1111"} {
		if isNonRoutable(net.ParseIP(pub)) {
			t.Errorf("isNonRoutable(%s) = true; a public IP is misclassified", pub)
		}
	}
}

// dupIP copies a net.IP (the slice form shares backing arrays).
func dupIP(ip net.IP) net.IP {
	out := make(net.IP, len(ip))
	copy(out, ip)
	return out
}

// incIP adds n to a 4- or 16-byte IP in place (big-endian).
func incIP(ip net.IP, n uint64) {
	for i := len(ip) - 1; i >= 0 && n > 0; i-- {
		sum := uint64(ip[i]) + (n & 0xff)
		ip[i] = byte(sum & 0xff)
		n >>= 8
		if sum > 0xff {
			n++ // carry
		}
	}
}

// countingFlippingResolver returns a public IP for the first lookup
// of each hostname and a private IP afterwards — the DNS-rebinding
// attack shape (the authoritative server answers differently over
// time). Per-host call counts drive the flip.
type countingFlippingResolver struct {
	calls map[string]int
}

func (f *countingFlippingResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[host]++
	if f.calls[host] > 1 {
		// The rebinding flip: same hostname, now private.
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
}

// proxyTo returns a RoundTripper that forwards every request to the
// given base URL (preserving path/query) — the test-side stand-in
// for "the pinned IP reaches the real server" without needing a
// real routable IP.
func proxyTo(baseURL string) http.RoundTripper {
	base, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	return &proxyTransport{base: base}
}

type proxyTransport struct{ base *url.URL }

func (p *proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	u.Scheme = p.base.Scheme
	u.Host = p.base.Host
	out, err := http.NewRequestWithContext(req.Context(), req.Method, u.String(), req.Body)
	if err != nil {
		return nil, err
	}
	out.Header = req.Header
	return http.DefaultTransport.RoundTrip(out)
}

// TestSSRF_RebindAcrossRedirectHops is the rebinding attack against
// the hop loop: hop 0 resolves public (allowed), the host redirects
// back to itself, and hop 1's re-evaluation sees the flipped
// (private) answer. The per-hop Policy.Eval is what catches it — the
// pre-M4-T7 design evaluated policy once and would have dialed the
// pinned transport for the second request without rechecking.
func TestSSRF_RebindAcrossRedirectHops(t *testing.T) {
	policy, err := NewPolicy("allow", nil, nil) // guard is the only barrier
	if err != nil {
		t.Fatal(err)
	}
	// The httptest server provides the redirect; the proxy transport
	// stands in for "the pinned IP reaches the real server".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/again")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	events := &recordingEvents{}
	c, err := NewClient(Options{
		Policy:           policy,
		Resolver:         &countingFlippingResolver{},
		Events:           events,
		RatePerMinute:    100,
		MaxResponseBytes: 1024,
		RoundTripper:     proxyTo(srv.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(context.Background(), "GET", "http://rebind.test/start")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrDeniedPrivateIP) {
		t.Fatalf("err = %v, want ErrDeniedPrivateIP at the rebind hop", err)
	}
	flips := c.(*httpClient).resolver.(*countingFlippingResolver)
	if flips.calls["rebind.test"] < 2 {
		t.Errorf("resolver calls = %d, want ≥2 (one per hop)", flips.calls["rebind.test"])
	}
	privateIP := events.recordedByName("private_ip")
	if len(privateIP) != 1 {
		t.Fatalf("private_ip events = %d, want 1", len(privateIP))
	}
}

// TestSSRF_RedirectToPrivateHostDenied proves the redirect variant
// of the rebinding attack: an allowlisted host redirects to a second
// *allowlisted* host whose DNS has flipped private. The hop loop's
// re-evaluation refuses before the dial, and the private_ip event
// carries the hop index.
func TestSSRF_RedirectToPrivateHostDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://flipped.test:9/steal")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	policy, err := NewPolicy("deny", []string{"allow.test", "flipped.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	port := urlPort(t, srv.URL)
	resolver := &fixedResolver{ips: map[string][]net.IP{
		// hop 0 resolves public (the guard stays armed); the proxy
		// transport forwards the pinned request to the real server.
		"allow.test":   {net.ParseIP("93.184.216.34")},
		"flipped.test": {net.ParseIP("10.0.0.9")}, // the flip
	}}
	events := &recordingEvents{}
	c, err := NewClient(Options{
		Policy:           policy,
		Resolver:         resolver,
		Events:           events,
		RatePerMinute:    100,
		MaxResponseBytes: 1024,
		RoundTripper:     proxyTo(srv.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(context.Background(), "GET", "http://allow.test:"+port+"/start")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrDeniedPrivateIP) {
		t.Fatalf("err = %v, want ErrDeniedPrivateIP at hop 1", err)
	}
	privateIP := events.recordedByName("private_ip")
	if len(privateIP) != 1 {
		t.Fatalf("private_ip events = %d, want 1", len(privateIP))
	}
	var hop int
	_ = json.Unmarshal(privateIP[0]["hop"], &hop)
	if hop != 1 {
		t.Errorf("private_ip hop = %d, want 1", hop)
	}
}

// TestSSRF_IPObfuscationHosts pins the integer-host forms a URL
// author can use to disguise a private-IP target:
//
//	http://2130706433/         (decimal — 127.0.0.1)
//	http://0x7f000001/         (hex)
//	http://0177.0.0.1/         (dotted octal)
//
// getaddrinfo resolves all three to 127.0.0.1; the resolver fake
// models exactly that. Two layers must both refuse: (a) the shipped
// default (deny + empty allowlist) refuses at the allowlist — the
// host string is not a listed hostname; (b) even under the
// documented allow-all escape hatch, the CIDR guard refuses at
// resolution. Neither layer trusts the caller's spelling.
func TestSSRF_IPObfuscationHosts(t *testing.T) {
	integerHosts := []struct {
		raw string
		ip  string
	}{
		{"http://2130706433/", "127.0.0.1"},
		{"http://0x7f000001/", "127.0.0.1"},
		{"http://0177.0.0.1/", "127.0.0.1"},
		{"http://3232235777/", "192.168.1.1"},
		{"http://0xA9FEFEFE/", "169.254.254.254"},
		{"http://2886729985/", "172.16.0.1"},
		{"http://2130706433./", "127.0.0.1"},
	}
	// Layer (a): shipped defaults — off-list refusal, audited as
	// denied_off_list.
	deny, err := NewPolicy("deny", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range integerHosts {
		res, err := deny.Eval(context.Background(), tc.raw, &fixedResolver{})
		if !errors.Is(err, ErrDeniedOffList) {
			t.Errorf("default-deny Eval(%s) err = %v, want ErrDeniedOffList", tc.raw, err)
		}
		if res.Decision != DecisionDeniedOffList {
			t.Errorf("default-deny Eval(%s) decision = %v, want denied_off_list", tc.raw, res.Decision)
		}
	}
	// Layer (b): allow-all escape hatch + a resolver that resolves
	// the integer form the way getaddrinfo does — private-IP refusal.
	allow, err := NewPolicy("allow", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range integerHosts {
		// Policy.Eval lowercases the host before resolving, so the
		// fake keys on the lowercased form (getaddrinfo is
		// case-insensitive; the attack works either way).
		host := strings.ToLower(rawHost(tc.raw))
		r := &fixedResolver{ips: map[string][]net.IP{host: {net.ParseIP(tc.ip)}}}
		res, err := allow.Eval(context.Background(), tc.raw, r)
		if !errors.Is(err, ErrDeniedPrivateIP) {
			t.Errorf("allow-all Eval(%s) err = %v, want ErrDeniedPrivateIP", tc.raw, err)
		}
		if res.Decision != DecisionDeniedPrivateIP {
			t.Errorf("allow-all Eval(%s) decision = %v, want denied_private_ip", tc.raw, res.Decision)
		}
	}
}

// rawHost extracts the URL's hostname for the resolver fake.
func rawHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// TestSSRF_ProductionWireNeverSkipsPrivateIPGuard is the
// ADR-0017 §4 promise: "a future M4-T8 regression test will assert
// the field is false in the production default." Two halves:
//
//  1. Behavioral — a Policy built by the production constructor
//     (NewPolicy, the only constructor the daemon uses) refuses a
//     private-IP resolution even under the allow-all escape hatch.
//  2. Structural — no production source under cmd/ references the
//     test-only escape hatch or the options constructor; the daemon
//     wire cannot disable the guard.
func TestSSRF_ProductionWireNeverSkipsPrivateIPGuard(t *testing.T) {
	// 1. Behavioral.
	p, err := NewPolicy("allow", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fixedResolver{ips: map[string][]net.IP{"loopback.test": {net.ParseIP("127.0.0.1")}}}
	if _, err := p.Eval(context.Background(), "https://loopback.test/", r); !errors.Is(err, ErrDeniedPrivateIP) {
		t.Fatalf("NewPolicy-built policy Eval err = %v, want ErrDeniedPrivateIP (the guard must be on in production)", err)
	}

	// 2. Structural: cmd/athanor must never reference the escape
	// hatch or the options constructor.
	entries, err := os.ReadDir("../../cmd/athanor")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join("../../cmd/athanor", name))
		if rerr != nil {
			t.Fatal(rerr)
		}
		for _, forbidden := range []string{"InsecureSkipPrivateIPGuard", "NewPolicyWithOptions"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("cmd/athanor/%s references %q; the production wire must never weaken the private-IP guard (ADR-0017 §4)",
					name, forbidden)
			}
		}
	}
}