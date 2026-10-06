// Cross-site request defense (ADR-0057).
//
// The Host-header allowlist (middleware.go, ADR-0011) closes DNS rebinding:
// an attacker-controlled hostname that resolves to 127.0.0.1 cannot be used
// because its Host header is not allowlisted. It does NOT close CSRF: a page
// the user visits can send a request to http://127.0.0.1:7420 with the
// legitimate Host header, and the browser attaches no preflight for a "simple
// request". Endpoints such as `POST /freeze` (no body) or the UI's form posts
// (`POST /ui/approvals/{id}`) would act on that request.
//
// This middleware closes the class by inspecting the browser-set
// cross-site signals on state-changing methods:
//
//   - Sec-Fetch-Site: same-origin | none  → allow (the daemon's own pages, or
//     a user-typed navigation).
//   - Neither Sec-Fetch-Site nor Origin   → allow (a non-browser client: the
//     CLI, a Job Pod, curl). Those have no ambient browser authority and are
//     separately bounded by the Host allowlist and, for pods, the bearer
//     token.
//   - Otherwise, allow only when Origin is exactly one of the daemon's own
//     origins; reject everything else with 403.
//
// The Origin set is derived from the Host allowlist so there is a single
// source of truth. Origin values are always scheme://host[:port] with no
// trailing slash; both http and https forms are accepted because the daemon
// binds plain loopback HTTP but a reverse proxy could front it with TLS.
package server

import (
	"net/http"
	"strings"
)

// isUnsafeMethod reports whether m is a state-changing method the cross-site
// guard must inspect. Safe methods (GET, HEAD, OPTIONS, TRACE) are never
// blocked: a cross-site GET cannot change state and the browser cannot read
// the response without CORS.
func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// originSet returns the accepted Origin values for every host:port in the
// allowlist, in both http and https forms.
func (h hostAllowlist) originSet() map[string]struct{} {
	out := make(map[string]struct{}, len(h.allowed)*2)
	for hostPort := range h.allowed {
		out["http://"+hostPort] = struct{}{}
		out["https://"+hostPort] = struct{}{}
	}
	return out
}

// crossSiteMiddleware rejects a browser-marked cross-site request to a
// state-changing route (ADR-0057). See the package comment for the decision
// table.
func (s *Server) crossSiteMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isUnsafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		site := r.Header.Get("Sec-Fetch-Site")
		if site == "same-origin" || site == "none" {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if site == "" && origin == "" {
			// Non-browser client (CLI, Job Pod, curl).
			next.ServeHTTP(w, r)
			return
		}
		if origin != "" {
			if _, ok := s.origins[strings.ToLower(origin)]; ok {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, `{"error":"cross-site request rejected"}`, http.StatusForbidden)
	})
}
