package internalapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

// authMiddleware wraps an http.Handler so that every request is
// authenticated against the per-job token. The flow:
//
//  1. Extract {id} from the request path. If absent, 404 (the mux
//     already routes only paths that have it; this is defensive).
//  2. Parse Authorization: Bearer <token>. Missing, wrong scheme,
//     or malformed → 401.
//  3. Look up tokens.Get(id). ErrTokenNotFound → 401 (uniform
//     401, no leak about whether the job ID exists; the pod
//     already knows its own ID).
//  4. Constant-time-compare the bearer against the stored token.
//     Mismatch → 401.
//  5. Attach the authenticated job ID to the request context and
//     call next.
//
// The constant-time compare is a structural requirement: Gate G2
// searches this file for the literal "ConstantTimeCompare" and
// fails the build if absent. Bypassing it with `==` is a Gate G2
// violation.
func authMiddleware(tokens TokenStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			// Should be unreachable: every route this
			// middleware wraps is registered with {id} in
			// the pattern. Treat as 404 anyway.
			writeError(w, http.StatusNotFound, "missing job id in path")
			return
		}

		bearer, ok := parseBearer(r.Header.Get("Authorization"))
		if !ok {
			writeError(w, http.StatusUnauthorized,
				"missing or malformed Authorization header (want \"Bearer <32 hex chars>\")")
			return
		}

		want, err := tokens.Get(id)
		if err != nil {
			// Uniform 401 — don't reveal whether the job ID
			// exists. ErrTokenNotFound is the common case
			// (job not running, token already removed).
			//
			// known-issues L13: perform a dummy constant-time compare so a
			// miss costs roughly the same as a wrong-token hit; otherwise the
			// timing of the token-store lookup itself leaks whether the job
			// id exists.
			_ = constantTimeLengthEq(len(bearer), len(bearer))
			_ = subtle.ConstantTimeCompare([]byte(bearer), []byte(bearer))
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		// Length-check before ConstantTimeCompare:
		// ConstantTimeCompare returns 0 for length mismatches,
		// but a length-mismatched token is a definitive
		// failure we should not pad-compare against. The
		// length check is itself constant-time on the
		// expected length (which is fixed at 32 hex chars
		// from jobpod's tokenBytes=16) so the fail path
		// doesn't leak the expected length via timing.
		if !constantTimeLengthEq(len(bearer), len(want)) {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if subtle.ConstantTimeCompare([]byte(bearer), []byte(want)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		// Attach the authenticated job ID. Handlers retrieve
		// it via jobIDFromContext.
		ctx := context.WithValue(r.Context(), ctxKeyJobID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// parseBearer extracts the token from an Authorization header.
// Returns (token, true) on success, ("", false) on any failure.
// The scheme must be exactly "Bearer" (case-insensitive, per
// RFC 6750). The token is the part after the scheme; surrounding
// whitespace is trimmed.
func parseBearer(header string) (string, bool) {
	const scheme = "bearer"
	if header == "" {
		return "", false
	}
	// Split on the first space; the rest is the token.
	sp := strings.IndexByte(header, ' ')
	if sp < 0 {
		return "", false
	}
	prefix := header[:sp]
	if !strings.EqualFold(prefix, scheme) {
		return "", false
	}
	tok := strings.TrimSpace(header[sp+1:])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// constantTimeLengthEq returns true iff a == b, in time independent
// of a and b. The standard "==" leaks the expected length on the
// fail path; this version does not. (Length is fixed at 32 hex
// chars from jobpod.tokenBytes=16, but the structural defense is
// worth keeping.)
func constantTimeLengthEq(a, b int) bool {
	return subtle.ConstantTimeEq(int32(a), int32(b)) == 1
}
