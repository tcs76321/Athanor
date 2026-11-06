package server

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// recoverMiddleware contains a handler panic to one request. The engine
// already contains panics inside a job; the HTTP surface needs the same
// property so a bad template value, a nil map, or a parser panic cannot drop
// the operator's connection with no explanation.
//
// The middleware preserves http.Flusher and http.ResponseController behavior
// for the SSE routes. If the response has already started (a stream wrote its
// 200 and some events), the status cannot be changed; the panic is logged and
// the connection is left to close. If nothing has been written, the caller
// receives a small 500 page instead of a reset.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &recoverWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("http: handler panicked",
					"method", r.Method, "path", r.URL.Path,
					"panic", rec, "stack", string(debug.Stack()))
				if rw.wroteHeader {
					return
				}
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Something went wrong. The Core is still running; retry the request or check the event log.\n"))
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// recoverWriter records whether the response has started and forwards Flush so
// the watch and dashboard streams keep working through the middleware.
type recoverWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *recoverWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *recoverWriter) Write(p []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(p)
}

// Flush keeps the SSE handlers' `w.(http.Flusher)` assertion true.
func (w *recoverWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer on Go 1.20+.
func (w *recoverWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
