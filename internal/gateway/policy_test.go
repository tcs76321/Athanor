// Tests for the §21.5 gateway's pure decision function
// (M4-T5, ADR-0017). The tests are table-driven; each row
// pins one observable behavior of `Policy.Eval` so a future
// refactor cannot silently regress. The network is not
// touched: the `Resolver` is a hand-written fake.
package gateway

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// fakeResolver is a Resolver that maps hostnames to
// pre-canned IP lists. Tests build one inline; the
// production code path is the same `LookupIPAddr` call
// the fake satisfies.
type fakeResolver struct {
	ips map[string][]net.IP
	err map[string]error
}

func (f *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if err, ok := f.err[host]; ok {
		return nil, err
	}
	raw, ok := f.ips[host]
	if !ok {
		// Default to 1.1.1.1 when the test does not
		// specify. A public, routable IP so the
		// "happy path" rows pass the DNS-rebinding
		// guard without ceremony.
		raw = []net.IP{net.ParseIP("1.1.1.1")}
	}
	out := make([]net.IPAddr, 0, len(raw))
	for _, ip := range raw {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

// staticResolver is a Resolver that returns a single fixed
// set of IPs for every host. Useful for the
// "every-host-resolves-privately" test row.
type staticResolver struct{ ips []net.IP }

func (s staticResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	out := make([]net.IPAddr, 0, len(s.ips))
	for _, ip := range s.ips {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

// TestNewPolicyValidation covers the constructor's
// rejection paths: unknown default_policy, empty entries,
// non-ASCII entries. The non-ASCII check is the §21.5
// ASCII-only constraint; a typo in the operator's config
// is caught at daemon boot, not at first fetch.
func TestNewPolicyValidation(t *testing.T) {
	cases := []struct {
		name    string
		def     string
		allow   []string
		deny    []string
		wantErr string
	}{
		{"bad_default", "open", nil, nil, "default_policy"},
		{"empty_allow_entry", "deny", []string{""}, nil, "empty entry"},
		{"empty_deny_entry", "deny", nil, []string{""}, "empty entry"},
		{"non_ascii_allow", "deny", []string{"例え.jp"}, nil, "non-ASCII"},
		{"non_ascii_deny", "deny", nil, []string{"例え.jp"}, "non-ASCII"},
		{"happy_deny", "deny", nil, nil, ""},
		{"happy_allow", "allow", nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewPolicy(c.def, c.allow, c.deny)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error %q does not contain %q", err, c.wantErr)
			}
		})
	}
}

// TestMatchHost pins the suffix-match gotcha that ADR-0017
// §3 calls out by name. The naive
// `strings.HasSuffix(host, "wikipedia.org")` matches
// `evilwikipedia.org`; the `.`-separator form does not.
// This test row is the durable backstop.
func TestMatchHost(t *testing.T) {
	cases := []struct {
		host, allow string
		want        bool
	}{
		// Exact matches
		{"example.org", "example.org", true},
		{"a.b.c.example.org", "a.b.c.example.org", true},
		// Subdomain matches (the dotted form)
		{"www.example.org", "example.org", true},
		{"a.b.c.example.org", "example.org", true},
		// The gotcha: must NOT match
		{"evilwikipedia.org", "wikipedia.org", false},
		{"example.org.evil.com", "example.org", false},
		// Case sensitivity (input is lowercased by
		// the caller; the function itself is
		// case-sensitive on purpose — the caller
		// normalizes)
		{"example.org", "EXAMPLE.ORG", false},
	}
	for _, c := range cases {
		t.Run(c.host+"/"+c.allow, func(t *testing.T) {
			if got := matchHost(c.host, c.allow); got != c.want {
				t.Errorf("matchHost(%q, %q) = %v, want %v", c.host, c.allow, got, c.want)
			}
		})
	}
}

// TestEval_DenyByDefault is the §21.5 default-deny knob's
// proof: with an empty allowlist and the shipped
// `default_policy: deny`, every host is refused.
func TestEval_DenyByDefault(t *testing.T) {
	p, err := NewPolicy("deny", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}
	res, err := p.Eval(context.Background(), "https://example.com/", r)
	if err == nil {
		t.Fatal("expected denial error, got nil")
	}
	if !errors.Is(err, ErrDeniedOffList) {
		t.Errorf("err = %v, want ErrDeniedOffList", err)
	}
	if res.Decision != DecisionDeniedOffList {
		t.Errorf("Decision = %v, want DecisionDeniedOffList", res.Decision)
	}
	if res.Reason != "denied_off_list" {
		t.Errorf("Reason = %q, want denied_off_list", res.Reason)
	}
}

// TestEval_AllowExactAndSubdomain covers the two positive
// shapes matchHost supports.
func TestEval_AllowExactAndSubdomain(t *testing.T) {
	p, err := NewPolicy("deny", []string{"wikipedia.org"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}
	cases := []string{
		"https://wikipedia.org/",
		"https://en.wikipedia.org/",
		"https://en.m.wikipedia.org/",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			res, err := p.Eval(context.Background(), u, r)
			if err != nil {
				t.Fatalf("Eval(%q) error: %v", u, err)
			}
			if res.Decision != DecisionAllowed {
				t.Errorf("Decision = %v, want DecisionAllowed", res.Decision)
			}
		})
	}
}

// TestEval_DenyTyposquat pins the `evilwikipedia.org`
// class of attack: a hostname that ends with the
// allowlisted suffix but is *not* a subdomain of it. The
// function must not allow it.
func TestEval_DenyTyposquat(t *testing.T) {
	p, err := NewPolicy("deny", []string{"wikipedia.org"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}
	res, err := p.Eval(context.Background(), "https://evilwikipedia.org/", r)
	if err == nil {
		t.Fatal("expected denial, got nil")
	}
	if !errors.Is(err, ErrDeniedOffList) {
		t.Errorf("err = %v, want ErrDeniedOffList", err)
	}
	if res.Decision != DecisionDeniedOffList {
		t.Errorf("Decision = %v, want DecisionDeniedOffList", res.Decision)
	}
}

// TestEval_DenyListOverridesAllow is the documented smell:
// an entry in deny_list wins over an entry in allow_list
// even when both match. The semantic is "explicit override
// wins."
func TestEval_DenyListOverridesAllow(t *testing.T) {
	p, err := NewPolicy("deny",
		[]string{"example.org"},
		[]string{"evil.example.org"},
	)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}

	// Plain example.org is allowed.
	if _, err := p.Eval(context.Background(), "https://example.org/", r); err != nil {
		t.Errorf("example.org denied: %v", err)
	}
	// evil.example.org is denied despite the suffix
	// match against example.org.
	res, err := p.Eval(context.Background(), "https://evil.example.org/", r)
	if !errors.Is(err, ErrDeniedDenyList) {
		t.Errorf("err = %v, want ErrDeniedDenyList", err)
	}
	if res.Decision != DecisionDeniedDenyList {
		t.Errorf("Decision = %v, want DecisionDeniedDenyList", res.Decision)
	}
}

// TestEval_DenyIDN pins the ASCII-only constraint. IDN
// is a T6+ concern; T5 fails closed.
func TestEval_DenyIDN(t *testing.T) {
	p, err := NewPolicy("allow", nil, nil) // allow + empty so the IDN check is the only barrier
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}
	res, err := p.Eval(context.Background(), "https://例え.jp/", r)
	if !errors.Is(err, ErrIDNNotSupported) {
		t.Errorf("err = %v, want ErrIDNNotSupported", err)
	}
	if res.Decision != DecisionDeniedIDN {
		t.Errorf("Decision = %v, want DecisionDeniedIDN", res.Decision)
	}
}

// TestEval_InvalidURL covers the URL parsing layer's
// rejection paths.
func TestEval_InvalidURL(t *testing.T) {
	p, err := NewPolicy("allow", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}
	cases := []struct {
		url, reason string
	}{
		{"://no-scheme", "invalid_url"},
		{"file:///etc/passwd", "invalid_url"},
		{"ftp://example.org/", "invalid_url"},
		{"https://", "invalid_url"},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			res, err := p.Eval(context.Background(), c.url, r)
			if !errors.Is(err, ErrInvalidURL) {
				t.Errorf("err = %v, want ErrInvalidURL", err)
			}
			if res.Decision != DecisionInvalidURL {
				t.Errorf("Decision = %v, want DecisionInvalidURL", res.Decision)
			}
		})
	}
}

// TestEval_DenyPrivateIP is the §21.5 DNS-rebinding
// guard. Each row pins a different non-routable range.
// A malicious allowlisted domain that flips its DNS to
// one of these IPs is refused before the TCP connect.
func TestEval_DenyPrivateIP(t *testing.T) {
	p, err := NewPolicy("allow", nil, nil) // allow so the resolver check is the only barrier
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ips  []net.IP
	}{
		{"loopback_v4", []net.IP{net.ParseIP("127.0.0.1")}},
		{"loopback_v6", []net.IP{net.ParseIP("::1")}},
		{"rfc1918_10", []net.IP{net.ParseIP("10.0.0.1")}},
		{"rfc1918_172", []net.IP{net.ParseIP("172.16.0.1")}},
		{"rfc1918_192", []net.IP{net.ParseIP("192.168.1.1")}},
		{"link_local", []net.IP{net.ParseIP("169.254.169.254")}}, // cloud metadata
		{"cgn", []net.IP{net.ParseIP("100.64.0.1")}},
		{"multicast", []net.IP{net.ParseIP("224.0.0.1")}},
		{"unspecified_v4", []net.IP{net.ParseIP("0.0.0.0")}},
		{"unspecified_v6", []net.IP{net.ParseIP("::")}},
		{"test_net_1", []net.IP{net.ParseIP("192.0.2.1")}},
		{"benchmarking", []net.IP{net.ParseIP("198.18.0.1")}},
		// Mixed: a public IP and a private IP — the
		// private one is enough to refuse.
		{"mixed_public_and_private", []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("10.0.0.1")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := staticResolver{ips: c.ips}
			res, err := p.Eval(context.Background(), "https://example.org/", r)
			if !errors.Is(err, ErrDeniedPrivateIP) {
				t.Errorf("err = %v, want ErrDeniedPrivateIP", err)
			}
			if res.Decision != DecisionDeniedPrivateIP {
				t.Errorf("Decision = %v, want DecisionDeniedPrivateIP", res.Decision)
			}
			if len(res.ResolvedIPs) != len(c.ips) {
				t.Errorf("ResolvedIPs len = %d, want %d", len(res.ResolvedIPs), len(c.ips))
			}
		})
	}
}

// TestEval_AllowPublicIPs is the positive complement of
// TestEval_DenyPrivateIP: a request that resolves to
// public IPs is allowed. The fakeResolver defaults to
// 1.1.1.1 when the test does not specify, so the
// "unspecified host" case covers the common path.
func TestEval_AllowPublicIPs(t *testing.T) {
	p, err := NewPolicy("allow", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := staticResolver{ips: []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("8.8.8.8")}}
	res, err := p.Eval(context.Background(), "https://example.org/", r)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if res.Decision != DecisionAllowed {
		t.Errorf("Decision = %v, want DecisionAllowed", res.Decision)
	}
	if len(res.ResolvedIPs) != 2 {
		t.Errorf("ResolvedIPs len = %d, want 2", len(res.ResolvedIPs))
	}
}

// TestEval_DefaultAllowEmptyAllowlist pins the documented
// escape hatch: `default_policy: allow` with an empty
// allow_list permits every host. This is the test-only
// path; production ships `default_policy: deny`.
func TestEval_DefaultAllowEmptyAllowlist(t *testing.T) {
	p, err := NewPolicy("allow", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}
	for _, u := range []string{
		"https://anything.test/",
		"https://another.test/path",
	} {
		if _, err := p.Eval(context.Background(), u, r); err != nil {
			t.Errorf("Eval(%q) = %v, want nil (default_policy=allow + empty allow_list)", u, err)
		}
	}
}

// TestEval_DefaultDenyDoesNotConsultResolver re-pins the
// §21.5 default-deny knob: with `default_policy: deny` and
// an empty allow_list, the resolver is *not* consulted. The
// fake resolver that would error on LookupIPAddr is
// therefore never called. This is the durability of the
// "fail closed at the policy layer" property.
func TestEval_DefaultDenyDoesNotConsultResolver(t *testing.T) {
	p, err := NewPolicy("deny", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{err: map[string]error{"example.org": errors.New("resolver must not be called")}}
	if _, err := p.Eval(context.Background(), "https://example.org/", r); !errors.Is(err, ErrDeniedOffList) {
		t.Errorf("err = %v, want ErrDeniedOffList", err)
	}
}


