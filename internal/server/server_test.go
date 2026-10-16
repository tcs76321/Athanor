package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// disableAllowlistForTest is a test helper that
// clears the ADR-0011 Host-header allowlist (the
// documented escape hatch in `SetHostAllowlist`).
// Tests that need a running test server but are not
// about the allowlist use this so the random
// httptest port does not fail the middleware check.
// The first ts is discarded and a new one started
// with the empty allowlist (the middleware is a
// no-op when the allowlist is empty).
func disableAllowlistForTest(t *testing.T, srv *Server, ts *httptest.Server) *httptest.Server {
	t.Helper()
	if err := srv.SetHostAllowlist(nil); err != nil {
		t.Fatal(err)
	}
	ts.Close()
	ts = httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestHealthz(t *testing.T) {
	srv := New("test-version")
	ts := disableAllowlistForTest(t, srv, httptest.NewServer(srv.Handler()))

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var body struct {
		Status        string  `json:"status"`
		Version       string  `json:"version"`
		Uptime        string  `json:"uptime"`
		UptimeSeconds float64 `json:"uptime_seconds"`
		Goroutines    int     `json:"goroutines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Version != "test-version" {
		t.Errorf("version = %q, want test-version", body.Version)
	}
	if body.Uptime == "" || body.UptimeSeconds < 0 {
		t.Errorf("uptime missing or invalid: %q / %v", body.Uptime, body.UptimeSeconds)
	}
	if body.Goroutines <= 0 {
		t.Errorf("goroutines = %d, want > 0 (M8-T31 soak sampling)", body.Goroutines)
	}
}

func TestHealthzMethodNotAllowed(t *testing.T) {
	srv := New("test-version")
	ts := disableAllowlistForTest(t, srv, httptest.NewServer(srv.Handler()))

	resp, err := http.Post(ts.URL+"/healthz", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Errorf("Allow header = %q, want GET", allow)
	}
}

func TestUnknownRoutes404(t *testing.T) {
	srv := New("test-version")
	ts := disableAllowlistForTest(t, srv, httptest.NewServer(srv.Handler()))

	resp, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown route status = %d, want 404", resp.StatusCode)
	}
}

func TestLocalhostAddr(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		fails bool
	}{
		{in: "127.0.0.1:7420", want: "127.0.0.1:7420"},
		{in: ":7420", want: "127.0.0.1:7420"},        // bare port → loopback
		{in: "0.0.0.0:8080", want: "127.0.0.1:8080"}, // wildcard → loopback
		{in: "[::]:8080", want: "127.0.0.1:8080"},    // IPv6 wildcard → loopback
		{in: "localhost:80", want: "localhost:80"},
		{in: "::1:80", fails: true}, // ambiguous without brackets is rejected by SplitHostPort
		{in: "192.168.1.5:80", fails: true},
		{in: "example.com:80", fails: true},
		{in: "noport", fails: true},
	}
	for _, tc := range cases {
		got, err := LocalhostAddr(tc.in)
		if tc.fails {
			if err == nil {
				t.Errorf("LocalhostAddr(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("LocalhostAddr(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("LocalhostAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
