package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverMiddlewareContainsPanic(t *testing.T) {
	srv := New("test")
	srv.Register("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	req.Host = "127.0.0.1:7420"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Something went wrong") {
		t.Errorf("body = %q, want a human error page", rec.Body.String())
	}
}

func TestRecoverMiddlewarePreservesFlusher(t *testing.T) {
	srv := New("test")
	srv.Register("/flush", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("wrapped writer lost http.Flusher; SSE routes would break")
		}
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/flush", nil)
	req.Host = "127.0.0.1:7420"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRecoverMiddlewareDoesNotRewriteStartedResponse(t *testing.T) {
	srv := New("test")
	srv.Register("/stream-then-panic", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("after headers")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream-then-panic", nil)
	req.Host = "127.0.0.1:7420"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the already-written 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "partial") {
		t.Errorf("body = %q, want the partial stream preserved", rec.Body.String())
	}
}
