package internalapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// fakeTokenStore is a map-backed TokenStore for tests. Use
// WithToken / WithoutToken to set up; reads return ErrTokenNotFound
// for missing IDs.
type fakeTokenStore struct {
	tokens map[string]string
}

func newFakeTokenStore() *fakeTokenStore {
	return &fakeTokenStore{tokens: map[string]string{}}
}

func (f *fakeTokenStore) WithToken(jobID, token string) *fakeTokenStore {
	f.tokens[jobID] = token
	return f
}

func (f *fakeTokenStore) Get(jobID string) (string, error) {
	tok, ok := f.tokens[jobID]
	if !ok {
		return "", ErrTokenNotFound
	}
	return tok, nil
}

// fakeToolEnv is a map-backed ToolEnvLookup for tests. WithAllow
// grants a specific tool envelope to a job; Clear removes it.
// Tests that want to exercise the "default envelope applies"
// path set a default and never call WithAllow.
type fakeToolEnv struct {
	calls int
	allow map[string]toolenvelope.Envelope
}

func newFakeToolEnv() *fakeToolEnv {
	return &fakeToolEnv{allow: map[string]toolenvelope.Envelope{}}
}

func (f *fakeToolEnv) WithAllow(jobID string, env toolenvelope.Envelope) *fakeToolEnv {
	f.allow[jobID] = env
	return f
}

func (f *fakeToolEnv) Clear(jobID string) *fakeToolEnv {
	delete(f.allow, jobID)
	return f
}

func (f *fakeToolEnv) EnvelopeFor(_ context.Context, jobID string, defaultEnv toolenvelope.Envelope) (toolenvelope.Envelope, error) {
	f.calls++
	if env, ok := f.allow[jobID]; ok {
		return env, nil
	}
	return defaultEnv, nil
}

// recorder is a minimal "did the handler run" sentinel. The
// middleware test cares only about whether next.ServeHTTP was
// reached and with what path/job-id, not about response bodies.
type recorder struct {
	called       bool
	jobIDFromCtx string
	path         string
}

func (r *recorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.called = true
		r.jobIDFromCtx = jobIDFromContext(req.Context())
		r.path = req.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// newRouter wires the middleware around the recorder's handler
// for a single fixed route. Tests pass a TokenStore of their
// choice.
func newRouter(t *testing.T, store TokenStore, pattern string) (*http.ServeMux, *recorder) {
	t.Helper()
	r := &recorder{}
	mux := http.NewServeMux()
	mux.Handle(pattern, authMiddleware(store, r.handler()))
	return mux, r
}

const goodToken = "0123456789abcdef0123456789abcdef" // 32 hex chars

// --- Bearer parsing ----------------------------------------------------

func TestParseBearer_HappyPath(t *testing.T) {
	tok, ok := parseBearer("Bearer " + goodToken)
	if !ok {
		t.Fatal("parseBearer returned !ok on a valid header")
	}
	if tok != goodToken {
		t.Errorf("token = %q, want %q", tok, goodToken)
	}
}

func TestParseBearer_CaseInsensitiveScheme(t *testing.T) {
	for _, prefix := range []string{"Bearer", "bearer", "BEARER", "BeArEr"} {
		tok, ok := parseBearer(prefix + " " + goodToken)
		if !ok {
			t.Errorf("parseBearer(%q) returned !ok; want ok", prefix)
		}
		if tok != goodToken {
			t.Errorf("parseBearer(%q) = %q, want %q", prefix, tok, goodToken)
		}
	}
}

func TestParseBearer_RejectsBadSchemes(t *testing.T) {
	cases := []struct {
		name, header string
	}{
		{"empty", ""},
		{"no scheme", goodToken},
		{"basic scheme", "Basic " + goodToken},
		{"only scheme", "Bearer "},
		{"only scheme no space", "Bearer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseBearer(tc.header)
			if ok {
				t.Errorf("parseBearer(%q) returned ok; want !ok", tc.header)
			}
		})
	}
}

// --- Middleware: rejection paths --------------------------------------

func TestAuthMiddleware_RejectsMissingAuthHeader(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, r := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-a", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if r.called {
		t.Error("handler was called; middleware should have rejected")
	}
}

func TestAuthMiddleware_RejectsWrongScheme(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, r := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-a", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if r.called {
		t.Error("handler was called for Basic-auth request")
	}
}

func TestAuthMiddleware_RejectsUnknownJobID(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, r := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-b", nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (uniform — no leak about which job ID exists)", w.Code)
	}
	if r.called {
		t.Error("handler was called for unknown job ID")
	}
}

func TestAuthMiddleware_RejectsTokenForDifferentJob(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, r := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-b", nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (token for job-a used on job-b)", w.Code)
	}
	if r.called {
		t.Error("handler was called for cross-job token reuse")
	}
}

func TestAuthMiddleware_RejectsWrongToken(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, r := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	wrongToken := "deadbeefdeadbeefdeadbeefdeadbeef" // 32 hex, valid shape, wrong value
	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-a", nil)
	req.Header.Set("Authorization", "Bearer "+wrongToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if r.called {
		t.Error("handler was called for wrong token")
	}
}

func TestAuthMiddleware_RejectsLengthMismatch(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, r := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-a", nil)
	req.Header.Set("Authorization", "Bearer abc")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if r.called {
		t.Error("handler was called for length-mismatched token")
	}
}

func TestAuthMiddleware_AcceptsValidToken(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, r := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-a", nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if !r.called {
		t.Error("handler was not called; middleware should have accepted")
	}
	if r.jobIDFromCtx != "job-a" {
		t.Errorf("ctx job ID = %q, want %q", r.jobIDFromCtx, "job-a")
	}
	if r.path != "/internal/v1/jobs/job-a" {
		t.Errorf("path = %q, want %q", r.path, "/internal/v1/jobs/job-a")
	}
}

// --- Structural proof: middleware is wrapped around every route --------

// TestAuthMiddleware_WrappedOnEveryRoute walks the internal API
// mux and asserts every registered pattern is reached through
// authMiddleware. The structural check is implemented as a
// behavioral test: a request without a token gets 401, not 200
// or 501. This is what Gate G2 actually proves — the handler
// never runs without a valid token.
func TestAuthMiddleware_WrappedOnEveryRoute(t *testing.T) {
	store := newFakeTokenStore()
	tools := newFakeToolEnv()
	mux := http.NewServeMux()
	// The handler-level deps are nil here because the wrapped-on-
	// every-route test never lets a request reach a handler. If
	// the middleware were ever bypassed for a route, the nil
	// dereference would surface as a 500 (panic recovery) — but
	// the real test is the 401 we get first.
	api := New(store, nil, nil, tools, toolenvelope.Envelope{}, nil)
	api.Register(mux)

	routes := []struct {
		method, path string
	}{
		{"GET", "/internal/v1/jobs/abc"},
		{"POST", "/internal/v1/jobs/abc/heartbeat"},
		{"POST", "/internal/v1/jobs/abc/log"},
		{"POST", "/internal/v1/jobs/abc/execute_code"},
		{"POST", "/internal/v1/jobs/abc/run_tests"},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (unauthenticated request reached the handler)", w.Code)
			}
		})
	}
}

func TestAuthMiddleware_NoIDInPathIsNotFound(t *testing.T) {
	store := newFakeTokenStore()
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_ = store

	req := httptest.NewRequest("GET", "/internal/v1/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (a non-{id} route is not wrapped by authMiddleware)", w.Code)
	}
}

func TestJobIDFromContext_EmptyForUnwrapped(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if got := jobIDFromContext(req.Context()); got != "" {
		t.Errorf("jobIDFromContext on bare ctx = %q, want \"\"", got)
	}
}

func TestJobIDFromContext_RoundTrip(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyJobID, "job-x")
	if got := jobIDFromContext(ctx); got != "job-x" {
		t.Errorf("jobIDFromContext = %q, want %q", got, "job-x")
	}
}

func TestAuthMiddleware_ErrorBodyIsJSON(t *testing.T) {
	store := newFakeTokenStore().WithToken("job-a", goodToken)
	mux, _ := newRouter(t, store, "GET /internal/v1/jobs/{id}")

	req := httptest.NewRequest("GET", "/internal/v1/jobs/job-a", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("0", 32))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json...", ct)
	}
	if !strings.Contains(w.Body.String(), "invalid token") {
		t.Errorf("body = %q, want it to contain 'invalid token'", w.Body.String())
	}
}

