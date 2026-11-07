package server

import "net/http"

// maxRequestBodyBytes bounds every request body on the daemon's external
// surface. The local API is loopback-only, but a stuck or hostile local
// client should not be able to make the Core allocate unbounded memory.
// Individual internal-API and UI handlers keep their tighter caps; this is
// the outer backstop.
const maxRequestBodyBytes = 8 << 20

// bodyLimitMiddleware wraps every request body with http.MaxBytesReader. The
// reader returns an error on the first read past the cap; handlers that
// decode or parse a form see that error and can answer honestly.
func bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
