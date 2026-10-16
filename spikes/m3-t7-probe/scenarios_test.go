package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeDaemon implements enough of the daemon surface for the conformance
// scenarios: the §22 freeze switch and the read-only UI pages + SSE stream.
func fakeDaemon(t *testing.T) *httptest.Server {
	t.Helper()
	var frozen atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/freeze", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]bool{"frozen": frozen.Load()})
		case http.MethodPost:
			frozen.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]bool{"frozen": true})
		case http.MethodDelete:
			var body struct {
				Reason string `json:"reason"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.TrimSpace(body.Reason) == "" {
				http.Error(w, `{"error":"reason required"}`, http.StatusBadRequest)
				return
			}
			frozen.Store(false)
			_ = json.NewEncoder(w).Encode(map[string]bool{"frozen": false})
		}
	})
	for _, p := range []string{"/ui", "/ui/statistics", "/ui/projects", "/ui/corrections"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><head><title>Athanor</title></head></html>"))
		})
	}
	mux.HandleFunc("/ui/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestScenarioFreeze(t *testing.T) {
	if got := scenarioFreeze(fakeDaemon(t).URL); !got.Pass {
		t.Errorf("scenarioFreeze = %+v, want pass", got)
	}
}

func TestScenarioUIPagesAndSSE(t *testing.T) {
	ts := fakeDaemon(t)
	if got := scenarioUIPages(ts.URL); !got.Pass {
		t.Errorf("scenarioUIPages = %+v, want pass", got)
	}
	if got := scenarioUISSE(ts.URL); !got.Pass {
		t.Errorf("scenarioUISSE = %+v, want pass", got)
	}
}

// TestScenarioFreezeFailsWhenNotFrozen pins that the scenario detects a
// regression (a daemon that does not actually freeze).
func TestScenarioFreezeFailsWhenNotFrozen(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"frozen": false})
	}))
	t.Cleanup(ts.Close)
	if got := scenarioFreeze(ts.URL); got.Pass {
		t.Errorf("scenarioFreeze passed against a daemon that never freezes: %+v", got)
	}
}

func TestRenderConformance(t *testing.T) {
	out := renderConformance([]scenarioResult{
		{Name: "freeze-unfreeze", Pass: true, Detail: "ok"},
		{Name: "ui-pages", Pass: false, Detail: "boom"},
	})
	for _, want := range []string{"Conformance", "PASS", "FAIL", "1/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("conformance render missing %q\n%s", want, out)
		}
	}
	if renderConformance(nil) != "" {
		t.Error("empty results must render nothing")
	}
}
