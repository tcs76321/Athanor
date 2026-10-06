// ADR-0057 cross-site request defense tests. The corpus covers the browser
// signals a cross-site attacker can and cannot control, plus the non-browser
// clients (CLI, Job Pod) that must keep working.
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// probeServer returns a Server with a method-agnostic /probe route that
// answers 200, so a "middleware allowed" request is observable as 200 and a
// rejection as 403.
func probeServer(t *testing.T) *Server {
	t.Helper()
	srv := New("test")
	srv.Register("/probe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return srv
}

// call runs one request through the full handler chain. host defaults to the
// allowlisted loopback entry; pass an explicit value to exercise the Host
// guard instead.
func call(t *testing.T, srv *Server, method, path, host string, hdr map[string]string) int {
	t.Helper()
	if host == "" {
		host = "127.0.0.1:7420"
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code
}

// TestCrossSite_RejectsCrossSiteSignals covers every browser-marked
// cross-site shape a state-changing request can arrive with.
func TestCrossSite_RejectsCrossSiteSignals(t *testing.T) {
	cases := []struct {
		name string
		hdr  map[string]string
	}{
		{"fetch no origin", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"form post with origin", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}},
		{"origin only (older browser)", map[string]string{"Origin": "https://evil.example"}},
		{"null origin (sandboxed frame)", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "null"}},
		{"same-site cross-origin", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://localhost:9999"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := probeServer(t)
			if got := call(t, srv, http.MethodPost, "/probe", "", c.hdr); got != http.StatusForbidden {
				t.Errorf("status = %d, want 403 Forbidden", got)
			}
		})
	}
}

// TestCrossSite_RejectsAllUnsafeMethods proves the guard is method-driven, not
// POST-only.
func TestCrossSite_RejectsAllUnsafeMethods(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			srv := probeServer(t)
			hdr := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
			if got := call(t, srv, m, "/probe", "", hdr); got != http.StatusForbidden {
				t.Errorf("%s status = %d, want 403", m, got)
			}
		})
	}
}

// TestCrossSite_AllowsLegitimateClients covers every shape that must keep
// working: the daemon's own pages, user navigations, and non-browser clients.
func TestCrossSite_AllowsLegitimateClients(t *testing.T) {
	cases := []struct {
		name string
		hdr  map[string]string
	}{
		{"same-origin ui form", map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{"user navigation", map[string]string{"Sec-Fetch-Site": "none"}},
		{"allowed origin", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://127.0.0.1:7420"}},
		{"allowed origin no site header", map[string]string{"Origin": "http://127.0.0.1:7420"}},
		{"cli no headers", nil},
		{"job pod no headers", map[string]string{"Authorization": "Bearer x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := probeServer(t)
			if got := call(t, srv, http.MethodPost, "/probe", "", c.hdr); got != http.StatusOK {
				t.Errorf("status = %d, want 200", got)
			}
		})
	}
}

// TestCrossSite_SafeMethodsUnaffected proves GET/HEAD/OPTIONS are never
// blocked even when the browser marks them cross-site.
func TestCrossSite_SafeMethodsUnaffected(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(m, func(t *testing.T) {
			srv := probeServer(t)
			hdr := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
			if got := call(t, srv, m, "/probe", "", hdr); got == http.StatusForbidden {
				t.Errorf("%s status = %d, want middleware-pass", m, got)
			}
		})
	}
}

// TestCrossSite_EmptyHostAllowlistStillRejects proves the empty-allowlist
// escape hatch disables only the Host check, not the CSRF guard: with no
// accepted origins a cross-site request is still refused, while a non-browser
// request passes.
func TestCrossSite_EmptyHostAllowlistStillRejects(t *testing.T) {
	srv := probeServer(t)
	if err := srv.SetHostAllowlist(nil); err != nil {
		t.Fatal(err)
	}
	if got := call(t, srv, http.MethodPost, "/probe", "example.com:7420",
		map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}); got != http.StatusForbidden {
		t.Errorf("cross-site status = %d, want 403", got)
	}
	if got := call(t, srv, http.MethodPost, "/probe", "example.com:7420", nil); got != http.StatusOK {
		t.Errorf("non-browser status = %d, want 200", got)
	}
}
