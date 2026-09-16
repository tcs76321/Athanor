package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestRedirectTarget_Table is the pure-function corpus for the hop
// follower's decision (ADR-0019 §3): 301/302/303 follow, 307/308 do
// not (method+body preservation is a capability no M4 consumer
// needs), relative Locations resolve against the base, and a
// Location that parses to a non-http(s) scheme or a URL without a
// host is not followed (fail-closed).
func TestRedirectTarget_Table(t *testing.T) {
	base := "https://allow.test/docs/start"
	cases := []struct {
		name       string
		status     int
		location   string
		wantFollow bool
		wantNext   string
	}{
		{"301 follows", 301, "/next", true, "https://allow.test/next"},
		{"302 follows", 302, "https://other.test/a", true, "https://other.test/a"},
		{"303 follows", 303, "b", true, "https://allow.test/docs/b"},
		{"307 not followed", 307, "/next", false, ""},
		{"308 not followed", 308, "/next", false, ""},
		{"200 not a redirect", 200, "/next", false, ""},
		{"missing location", 302, "", false, ""},
		{"unparsable location", 302, "http://[", false, ""},
		{"non-http scheme", 302, "ftp://allow.test/f", false, ""},
		{"no host after resolution", 302, "mailto:x@y", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseURL, err := url.Parse(base)
			if err != nil {
				t.Fatal(err)
			}
			res := &Response{StatusCode: tc.status, Header: http.Header{}}
			if tc.location != "" {
				res.Header.Set("Location", tc.location)
			}
			next, follow := redirectTarget(baseURL, res)
			if follow != tc.wantFollow {
				t.Fatalf("follow = %v, want %v", follow, tc.wantFollow)
			}
			if tc.wantFollow && next != tc.wantNext {
				t.Errorf("next = %q, want %q", next, tc.wantNext)
			}
		})
	}
}

// allowOnlyPolicy returns a policy whose allowlist contains exactly
// `host` with the private-IP guard skipped (the ADR-0017 §4
// test-only escape hatch — the httptest server binds 127.0.0.1).
func allowOnlyPolicy(t *testing.T, host string) *Policy {
	t.Helper()
	p, err := NewPolicyWithOptions(PolicyOptions{
		DefaultPolicy:              "deny",
		AllowList:                  []string{host},
		InsecureSkipPrivateIPGuard: true,
	})
	if err != nil {
		t.Fatalf("NewPolicyWithOptions: %v", err)
	}
	return p
}

// resolverForHost maps a fake hostname to the httptest server's IP
// so the pinned dial lands on the test server while the policy
// evaluates the fake hostname (the mechanism the off-list-redirect
// test uses to distinguish allowlisted hop 0 from off-list hop 1).
func resolverForHost(host, srvURL string) Resolver {
	u, err := url.Parse(srvURL)
	if err != nil {
		return &fixedResolver{}
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return &fixedResolver{}
	}
	return &fixedResolver{ips: map[string][]net.IP{host: {ip}}}
}

// urlPort returns the port of an httptest server URL.
func urlPort(t *testing.T, srvURL string) string {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", srvURL, err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", u.Host, err)
	}
	return port
}

// TestFetch_FollowsAllowlistedRedirect proves the hop loop follows a
// relative 302 within an allowed host: the final response is the
// target's body, and the audit log carries one event per hop with
// the hop index and, from hop 1 on, the accumulated redirect chain.
func TestFetch_FollowsAllowlistedRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/final")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("final body"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, events, _ := newTestClient(t, newTestPolicy(t), resolverOpt(srv.URL))
	req, err := NewRequest(context.Background(), "GET", srv.URL+"/start")
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != "final body" {
		t.Errorf("body = %q, want %q", res.Body, "final body")
	}
	if !strings.HasSuffix(res.URL, "/final") {
		t.Errorf("URL = %q, want the final hop's URL", res.URL)
	}

	fetched := events.recordedByName("fetched")
	if len(fetched) != 2 {
		t.Fatalf("fetched events = %d, want 2 (one per hop)", len(fetched))
	}
	var hop0, hop1 int
	var chain []string
	for _, ev := range fetched {
		var hop int
		_ = json.Unmarshal(ev["hop"], &hop)
		switch hop {
		case 0:
			hop0 = 1
		case 1:
			hop1 = 1
			_ = json.Unmarshal(ev["redirect_chain"], &chain)
		}
	}
	if hop0 != 1 || hop1 != 1 {
		t.Errorf("hop events: hop0=%d hop1=%d, want 1 and 1", hop0, hop1)
	}
	if len(chain) != 1 || !strings.HasSuffix(chain[0], "/start") {
		t.Errorf("redirect_chain = %v, want the hop-0 URL", chain)
	}
}

// TestFetch_DeniesOffListRedirect is the redirect-SSRF regression
// proof (the gap ADR-0019 §3 closes): hop 0 is allowlisted, the
// Location points at a different (off-list) host, and the hop loop
// must refuse before dialing. The policy allows only `allow.test`;
// the resolver maps that hostname to the httptest server's IP so
// hop 0 actually reaches the server, whose handler redirects to
// `http://evil.test/` — an off-list host. The fake URL carries the
// server's real port: the pinned dial takes its port from the
// request address, and the policy matches on hostname only.
func TestFetch_DeniesOffListRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://evil.test/steal")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	port := urlPort(t, srv.URL)
	p := allowOnlyPolicy(t, "allow.test")
	events := &recordingEvents{}
	c, err := NewClient(Options{
		Policy:           p,
		Resolver:         resolverForHost("allow.test", srv.URL),
		Events:           events,
		RatePerMinute:    10,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(context.Background(), "GET", "http://allow.test:"+port+"/start")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrDeniedOffList) {
		t.Fatalf("err = %v, want ErrDeniedOffList", err)
	}

	// Hop 0 succeeded (event=fetched); hop 1 was refused with the
	// ordinary denial event naming the off-list host.
	denied := events.recordedByName("denied")
	if len(denied) != 1 {
		t.Fatalf("denied events = %d, want 1", len(denied))
	}
	var rawURL string
	_ = json.Unmarshal(denied[0]["url"], &rawURL)
	if !strings.Contains(rawURL, "evil.test") {
		t.Errorf("denied url = %q, want the off-list redirect target", rawURL)
	}
	var hop int
	_ = json.Unmarshal(denied[0]["hop"], &hop)
	if hop != 1 {
		t.Errorf("denied hop = %d, want 1", hop)
	}
}

// TestFetch_RedirectLoopHitsCap proves the hop cap: a self-redirect
// between two allowlisted pages cannot spin; Fetch aborts with
// ErrTooManyRedirects after maxRedirectHops.
func TestFetch_RedirectLoopHitsCap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/loop", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/loop")
		w.WriteHeader(http.StatusMovedPermanently)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _, _ := newTestClient(t, newTestPolicy(t), resolverOpt(srv.URL), func(o *Options) {
		// Each hop consumes a rate-limit token; the loop cap (5)
		// needs a bucket bigger than newTestClient's default of 2.
		o.RatePerMinute = 100
	})
	req, err := NewRequest(context.Background(), "GET", srv.URL+"/loop")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("err = %v, want ErrTooManyRedirects", err)
	}
}

// TestFetch_TemporaryRedirectNotFollowed proves 307 is returned to
// the caller unfollowed (redirectStatuses deliberately omits it —
// method+body preservation is not an M4 capability).
func TestFetch_TemporaryRedirectNotFollowed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/307", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/target")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/target", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("should not be fetched"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _, _ := newTestClient(t, newTestPolicy(t), resolverOpt(srv.URL))
	req, err := NewRequest(context.Background(), "GET", srv.URL+"/307")
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want 307 returned unfollowed", res.StatusCode)
	}
}