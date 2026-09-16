package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The M4-T8 allowlist-bypass corpus (ROADMAP M4-T8, §31.3). The T5
// corpus pinned the suffix-match gotcha; this suite assembles the
// full attack set an operator's allowlist faces — typosquats,
// userinfo tricks, trailing dots, scheme smuggling, control
// characters, IDN — and proves two things per denial: the typed
// error class, and the matching `network` audit row (the acceptance
// criterion's "events logged under network" half).
//
// Positive controls are pinned too: uppercase hosts and explicit
// ports are legitimate spellings of an allowlisted hostname and must
// remain allowed — an over-broad "fix" that broke them would be its
// own regression.

// bypassPolicy allows exactly wikipedia.org (suffix-match with the
// explicit "." separator) and blocks one subdomain explicitly.
func bypassPolicy(t *testing.T) *Policy {
	t.Helper()
	p, err := NewPolicy("deny", []string{"wikipedia.org"}, []string{"blocked.wikipedia.org"})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return p
}

// TestBypass_PolicyDecisions is the decision corpus at the Policy
// layer. Every row names the attack, the expected sentinel, and the
// expected audit `decision` reason.
func TestBypass_PolicyDecisions(t *testing.T) {
	p := bypassPolicy(t)
	cases := []struct {
		name         string
		rawURL       string
		wantErr      error
		wantDecision string
	}{
		// --- attacks -------------------------------------------------
		{"typosquat suffix", "https://evilwikipedia.org/", ErrDeniedOffList, "denied_off_list"},
		{"embedded suffix", "https://wikipedia.org.evil.test/", ErrDeniedOffList, "denied_off_list"},
		{"userinfo host confusion", "https://wikipedia.org@evil.test/", ErrDeniedOffList, "denied_off_list"},
		{"trailing dot is not the listed host", "https://wikipedia.org./", ErrDeniedOffList, "denied_off_list"},
		{"file scheme", "file:///etc/passwd", ErrInvalidURL, "invalid_url"},
		{"javascript scheme", "javascript:alert(1)", ErrInvalidURL, "invalid_url"},
		{"data scheme", "data:text/html,<h1>x</h1>", ErrInvalidURL, "invalid_url"},
		{"gopher scheme", "gopher://evil.test:70/", ErrInvalidURL, "invalid_url"},
		{"websocket scheme", "ws://evil.test/", ErrInvalidURL, "invalid_url"},
		{"empty host", "https:///only/a/path", ErrInvalidURL, "invalid_url"},
		{"CRLF injection in path", "https://wikipedia.org/a\r\nX-Evil: 1", ErrInvalidURL, "invalid_url"},
		{"IDN host", "https://wíkipedia.org/", ErrIDNNotSupported, "denied_idn"},
		// --- positive controls (legitimate spellings stay allowed) ---
		{"uppercase host", "https://WIKIPEDIA.ORG/wiki/Main_Page", nil, "allowed"},
		{"explicit port", "https://wikipedia.org:443/wiki/Main_Page", nil, "allowed"},
		{"subdomain suffix", "https://en.wikipedia.org/wiki/Main_Page", nil, "allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := p.Eval(context.Background(), tc.rawURL, &fixedResolver{})
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Eval err = %v, want allowed", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Eval err = %v, want %v", err, tc.wantErr)
			}
			if res.Reason != tc.wantDecision {
				t.Errorf("reason = %q, want %q", res.Reason, tc.wantDecision)
			}
		})
	}
}

// TestBypass_DenialsAreAudited runs the denial rows through the full
// Client (with a transport that panics if reached — every denial must
// short-circuit before the dial) and asserts the `network` audit row
// carries the decision reason and the offending URL.
func TestBypass_DenialsAreAudited(t *testing.T) {
	rows := []struct {
		name         string
		rawURL       string
		wantDecision string
		auditURL     string // defaults to rawURL; the request layer escapes some spellings
	}{
		{"typosquat", "https://evilwikipedia.org/", "denied_off_list", ""},
		{"userinfo", "https://wikipedia.org@evil.test/", "denied_off_list", ""},
		{"trailing dot", "https://wikipedia.org./", "denied_off_list", ""},
		// http.NewRequest percent-escapes the non-ASCII host before
		// the fetch; the audit row records the escaped form.
		{"IDN", "https://wíkipedia.org/", "denied_idn", "https://w%C3%ADkipedia.org/"},
	}
	p := bypassPolicy(t)
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			events := &recordingEvents{}
			c, err := NewClient(Options{
				Policy:           p,
				Resolver:         &fixedResolver{},
				Events:           events,
				RatePerMinute:    100,
				MaxResponseBytes: 1024,
				RoundTripper:     failingRoundTripper{},
			})
			if err != nil {
				t.Fatal(err)
			}
			req, err := NewRequest(context.Background(), "GET", row.rawURL)
			if err != nil {
				// The URL is rejected before the gateway layer; that
				// is itself a fail-closed outcome — nothing to audit
				// because the gateway never saw it.
				t.Skipf("request construction rejected the URL (%v)", err)
			}
			if _, err := c.Fetch(context.Background(), req); err == nil {
				t.Fatal("Fetch returned nil error, want a denial")
			}
			denied := events.recordedByName("denied")
			if len(denied) != 1 {
				t.Fatalf("denied events = %d, want 1", len(denied))
			}
			var decision, url string
			_ = json.Unmarshal(denied[0]["decision"], &decision)
			_ = json.Unmarshal(denied[0]["url"], &url)
			if decision != row.wantDecision {
				t.Errorf("audit decision = %q, want %q", decision, row.wantDecision)
			}
			if url != row.rawURL {
				want := row.rawURL
				if row.auditURL != "" {
					want = row.auditURL
				}
				if url != want {
					t.Errorf("audit url = %q, want %q", url, want)
				}
			}
		})
	}
}

// TestBypass_DenyListWinsOnRedirectHop proves the explicit-override
// survives the hop loop: an allowed host redirects to a deny-listed
// subdomain; hop 1's re-evaluation refuses with denied_denylist.
func TestBypass_DenyListWinsOnRedirectHop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://blocked.wikipedia.org:9/steal")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	p := bypassPolicy(t)
	port := urlPort(t, srv.URL)
	resolver := &fixedResolver{ips: map[string][]net.IP{
		"wikipedia.org":          {net.ParseIP("93.184.216.34")},
		"blocked.wikipedia.org": {net.ParseIP("93.184.216.35")},
	}}
	events := &recordingEvents{}
	c, err := NewClient(Options{
		Policy:           p,
		Resolver:         resolver,
		Events:           events,
		RatePerMinute:    100,
		MaxResponseBytes: 1024,
		RoundTripper:     proxyTo(srv.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(context.Background(), "GET", "http://wikipedia.org:"+port+"/start")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Fetch(context.Background(), req)
	if !errors.Is(err, ErrDeniedDenyList) {
		t.Fatalf("err = %v, want ErrDeniedDenyList at hop 1", err)
	}
	denied := events.recordedByName("denied")
	if len(denied) != 1 {
		t.Fatalf("denied events = %d, want 1", len(denied))
	}
	var decision string
	_ = json.Unmarshal(denied[0]["decision"], &decision)
	if decision != "denied_denylist" {
		t.Errorf("audit decision = %q, want denied_denylist", decision)
	}
}