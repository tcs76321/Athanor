// Package server provides the daemon's HTTP surface. Per ARCHITECTURE
// §21.8 it binds to loopback only; remote access is via SSH port
// forwarding. M0-T7 ships /healthz; M1 attaches the kill-switch control
// routes to the same mux.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Control is the kill-switch surface the HTTP layer drives (§22). It is
// satisfied by *control.KillSwitch.
type Control interface {
	Frozen() bool
	Freeze(ctx context.Context) error
	Unfreeze(ctx context.Context, reason string) error
}

// Server holds daemon-wide HTTP state.
type Server struct {
	version       string
	started       time.Time
	control       Control
	mux           *http.ServeMux
	hostAllowlist hostAllowlist
	// origins is the set of browser Origin values the cross-site guard
	// (ADR-0057) accepts for state-changing requests. It is derived from
	// the same host allowlist, so there is one source of truth.
	origins map[string]struct{}
}

// defaultHostAllowlist is the §D1 set from ADR-0011.
// Used by New() when SetHostAllowlist is not called
// (the common case; tests that want to exercise the
// allowlist bypass this constructor).
var defaultHostAllowlist = []string{
	"127.0.0.1:7420",
	"localhost:7420",
	"[::1]:7420",
	"athanor.local:7420",
}

// New returns a Server whose uptime clock starts now.
// The default Host-header allowlist (ADR-0011 §D1) is
// installed; callers that need a different set use
// SetHostAllowlist.
func New(version string) *Server {
	s := &Server{version: version, started: time.Now(), mux: http.NewServeMux()}
	// newHostAllowlist on the static default list is
	// guaranteed to succeed (the package init in
	// middleware.go is a compile-time sanity check).
	hl, _ := newHostAllowlist(defaultHostAllowlist)
	s.hostAllowlist = hl
	s.origins = hl.originSet()
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	return s
}

// SetHostAllowlist replaces the default allowlist.
// An empty list disables the check (documented
// escape hatch for tests; never the default in
// production). An invalid entry is a build-time
// error from newHostAllowlist and bubbles up
// here.
func (s *Server) SetHostAllowlist(entries []string) error {
	hl, err := newHostAllowlist(entries)
	if err != nil {
		return err
	}
	s.hostAllowlist = hl
	s.origins = hl.originSet()
	return nil
}

// SetControl attaches the kill switch. Without it the control routes are
// not registered — a daemon with no control surface cannot be frozen.
func (s *Server) SetControl(c Control) {
	s.control = c
	s.mux.HandleFunc("/freeze", s.handleFreeze)
}

// Register attaches additional routes (e.g. the M1 API) to the same
// loopback-only mux.
func (s *Server) Register(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// Mux exposes the underlying mux for packages that register many routes
// at once (internal/api).
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Handler returns the root http.Handler with all routes attached.
//
// Two middlewares wrap the mux (ARCHITECTURE §21.8; ADR-0011, ADR-0057):
//
//   - hostMiddleware enforces the Host-header allowlist (the DNS-rebinding
//     defense); it is skipped when the allowlist is empty (the documented
//     test escape hatch).
//   - crossSiteMiddleware rejects a browser-marked cross-site request to a
//     state-changing route (CSRF); it is always active.
//
// The internal API (/internal/v1/) shares this mux and is wrapped by both,
// but its callers are Job Pods and the CLI, which send neither Origin nor
// Sec-Fetch-Site, so the cross-site guard is a no-op there; its own
// bearer-token middleware (ADR-0008) remains the pod-facing defense.
func (s *Server) Handler() http.Handler {
	h := s.crossSiteMiddleware(s.mux)
	if len(s.hostAllowlist.allowed) == 0 {
		return h
	}
	return s.hostMiddleware(h)
}

type healthResponse struct {
	Status        string  `json:"status"`
	Version       string  `json:"version"`
	Uptime        string  `json:"uptime"`
	UptimeSeconds float64 `json:"uptime_seconds"`
}

// handleHealthz reports status, version, and uptime (M0-T7 acceptance).
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uptime := time.Since(s.started)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:        "ok",
		Version:       s.version,
		Uptime:        uptime.Round(time.Second).String(),
		UptimeSeconds: uptime.Seconds(),
	})
}

type freezeResponse struct {
	Frozen  bool   `json:"frozen"`
	Message string `json:"message,omitempty"`
}

// handleFreeze serves GET /freeze (status), POST /freeze (freeze), and
// DELETE /freeze (unfreeze with a reason). Freezing is idempotent;
// unfreezing without a reason is rejected (§22.2).
func (s *Server) handleFreeze(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(freezeResponse{Frozen: s.control.Frozen()})
	case http.MethodPost:
		if err := s.control.Freeze(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(freezeResponse{
			Frozen:  true,
			Message: "frozen: no new work will start; unfreeze requires a reason (DELETE /freeze with {\"reason\": \"...\"})",
		})
	case http.MethodDelete:
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
			http.Error(w, `{"error":"body must be JSON: {\"reason\":\"...\"}"}`, http.StatusBadRequest)
			return
		}
		if err := s.control.Unfreeze(r.Context(), body.Reason); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(freezeResponse{Frozen: false})
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// LocalhostAddr forces a listen address onto the loopback interface
// (§21.8). Bare ports and wildcard hosts become 127.0.0.1; explicit
// non-loopback hosts are rejected outright (fail closed).
func LocalhostAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("listen address %q must be host:port: %w", addr, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	case "localhost", "127.0.0.1", "::1":
	default:
		return "", fmt.Errorf("listen address %q must bind localhost only (ARCHITECTURE §21.8)", addr)
	}
	return net.JoinHostPort(host, port), nil
}
