package internalapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
	"github.com/tcs76321/athanor/migrations"
)

// newHandlerTestEnv wires a real SQLite-backed store + project
// repo + API + mux + a token store pre-loaded with one job's
// token. The handler tests use this to exercise the full
// request/response cycle.
type handlerTestEnv struct {
	api    *API
	mux    *http.ServeMux
	store  *store.Store
	tokens *fakeTokenStore
	repo   *project.Repo
	tools  *fakeToolEnv
}

func newHandlerTestEnv(t *testing.T) *handlerTestEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	repo := project.NewRepo(st)
	tokens := newFakeTokenStore()
	tools := newFakeToolEnv()
	// Default envelope is empty; tests that want to grant tools
	// call env.tools.WithAllow(taskID, toolenvelope.Parse(...))
	// explicitly. This mirrors the production default where
	// config.job_pod.default_tools is an empty list.
	// The gateway is nil by default; gateway_tools_test.go uses
	// newHandlerTestEnvWithGateway for the dispatch tests.
	api := New(tokens, repo, st, tools, toolenvelope.Envelope{}, nil)
	mux := http.NewServeMux()
	api.Register(mux)
	return &handlerTestEnv{api: api, mux: mux, store: st, tokens: tokens, repo: repo, tools: tools}
}

// seedProject creates a project with a text goal and returns
// (projectID, taskID).
func (e *handlerTestEnv) seedProject(t *testing.T) (string, string) {
	t.Helper()
	_, task, err := e.repo.Create(context.Background(),
		"handler-test", "text",
		"Write a short essay about local-first software.",
		[]string{"at least three arguments", "a conclusion"},
	)
	if err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	return task.ProjectID, task.ID
}

// --- GET /internal/v1/jobs/{id} ---------------------------------------

func TestHandleJobGet_RealRoundTrip(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	req := httptest.NewRequest("GET", "/internal/v1/jobs/"+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var body jobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %s", err, w.Body.String())
	}
	if body.ID != taskID {
		t.Errorf("body.ID = %q, want %q", body.ID, taskID)
	}
	if body.TaskTitle == "" {
		t.Errorf("body.TaskTitle is empty; want non-empty (the goal text)")
	}
	if len(body.Criteria) != 2 {
		t.Errorf("body.Criteria has %d items, want 2", len(body.Criteria))
	}
	if body.State != "running" {
		t.Errorf("body.State = %q, want %q", body.State, "running")
	}
}

func TestHandleJobGet_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	req := httptest.NewRequest("GET", "/internal/v1/jobs/"+taskID, nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleJobGet_RejectsCrossJobToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	// Token registered for a different job ID.
	env.tokens.WithToken("other-job", goodToken)

	req := httptest.NewRequest("GET", "/internal/v1/jobs/"+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
// --- POST /internal/v1/jobs/{id}/heartbeat ----------------------------

func TestHandleHeartbeat_WritesEvent(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	events, err := env.store.QueryEvents(context.Background(), store.EventFilter{JobID: taskID})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	var found bool
	for _, e := range events {
		var d struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal([]byte(e.DataJSON), &d)
		if d.Event == "heartbeat" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no heartbeat event recorded; events = %+v", events)
	}
}

func TestHandleHeartbeat_AcceptsEmptyBody(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (empty body should be allowed)", w.Code)
	}
}

func TestHandleHeartbeat_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/heartbeat", nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// --- POST /internal/v1/jobs/{id}/log ----------------------------------

func TestHandleLog_AppendsEvent(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	body, _ := json.Marshal(logRequest{Stream: "stdout", Line: "hello from the pod"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/log", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	events, err := env.store.QueryEvents(context.Background(), store.EventFilter{JobID: taskID})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	var found bool
	for _, e := range events {
		var d struct {
			Event  string `json:"event"`
			Stream string `json:"stream"`
			Line   string `json:"line"`
		}
		_ = json.Unmarshal([]byte(e.DataJSON), &d)
		if d.Event == "pod_log" && d.Line == "hello from the pod" && d.Stream == "stdout" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no pod_log event with the right line recorded; events = %+v", events)
	}
}

func TestHandleLog_DefaultsStreamToStdout(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	body, _ := json.Marshal(logRequest{Line: "no stream field"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/log", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	events, _ := env.store.QueryEvents(context.Background(), store.EventFilter{JobID: taskID})
	for _, e := range events {
		var d struct {
			Event  string `json:"event"`
			Stream string `json:"stream"`
		}
		_ = json.Unmarshal([]byte(e.DataJSON), &d)
		if d.Event == "pod_log" && d.Stream != "stdout" {
			t.Errorf("default stream = %q, want %q", d.Stream, "stdout")
		}
	}
}

func TestHandleLog_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	body, _ := json.Marshal(logRequest{Line: "unauthenticated"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/log", bytes.NewReader(body))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleLog_RejectsMalformedBody(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/log",
		bytes.NewReader([]byte("{not json")))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

