package internalapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// fakeQuerier is a MemoryQuerier for the M5-T7 handler tests.
type fakeQuerier struct {
	reqs []toolenvelope.QueryMemoryRequest
	res  *toolenvelope.QueryMemoryResponse
	err  error
}

func (f *fakeQuerier) QueryMemory(_ context.Context, req toolenvelope.QueryMemoryRequest) (*toolenvelope.QueryMemoryResponse, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.res, nil
}

func newHandlerTestEnvWithQuerier(t *testing.T, q MemoryQuerier) *handlerTestEnv {
	t.Helper()
	env := newHandlerTestEnv(t)
	env.api.querier = q
	return env
}

func TestQueryMemory_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/query_memory",
		bytes.NewReader([]byte(`{"query":"anything"}`)))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no bearer)", w.Code)
	}
}

func TestQueryMemory_RejectsWhenToolNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnvWithQuerier(t, &fakeQuerier{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	body, _ := json.Marshal(toolenvelope.QueryMemoryRequest{Query: "anything"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/query_memory", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(ErrToolDisallowed.Error())) {
		t.Errorf("body = %q, want ErrToolDisallowed text", w.Body.String())
	}
	if got := countToolEvents(t, env, taskID, "tool_disallowed", "query_memory"); got != 1 {
		t.Errorf("tool_disallowed events = %d, want 1", got)
	}
}

func TestQueryMemory_HappyPath(t *testing.T) {
	q := &fakeQuerier{res: &toolenvelope.QueryMemoryResponse{
		Query: "parser", Scope: "ignored", VectorEnabled: true,
		Hits: []toolenvelope.MemoryHitWire{
			{ID: "cm-1", Kind: "memo", Score: 0.032, BM25Rank: 1, Content: "the parser"},
		},
	}}
	env := newHandlerTestEnvWithQuerier(t, q)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "query_memory"))

	body, _ := json.Marshal(toolenvelope.QueryMemoryRequest{Query: "  parser  "})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/query_memory", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(q.reqs) != 1 {
		t.Fatalf("querier calls = %d, want 1", len(q.reqs))
	}
	if q.reqs[0].Tool != toolenvelope.ToolQueryMemory {
		t.Errorf("tool = %q, want query_memory", q.reqs[0].Tool)
	}
	if q.reqs[0].Scope != taskID {
		t.Errorf("scope = %q, want the authenticated job id %q", q.reqs[0].Scope, taskID)
	}
	if q.reqs[0].Query != "parser" {
		t.Errorf("query = %q, want the trimmed query", q.reqs[0].Query)
	}
	if got := countToolEvents(t, env, taskID, "tool_call", "query_memory"); got != 1 {
		t.Errorf("tool_call events = %d, want 1", got)
	}
	var res toolenvelope.QueryMemoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ID != "cm-1" || !res.VectorEnabled {
		t.Errorf("response = %+v", res)
	}
}

func TestQueryMemory_RejectsEmptyQuery(t *testing.T) {
	env := newHandlerTestEnvWithQuerier(t, &fakeQuerier{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "query_memory"))

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/query_memory",
		bytes.NewReader([]byte(`{"query":"   "}`)))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func TestQueryMemory_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"scope required", ErrMemoryScopeRequired, http.StatusBadRequest},
		{"memory disabled", ErrMemoryDisabled, http.StatusNotImplemented},
		{"unexpected failure", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newHandlerTestEnvWithQuerier(t, &fakeQuerier{err: c.err})
			_, taskID := env.seedProject(t)
			env.tokens.WithToken(taskID, goodToken)
			env.tools.WithAllow(taskID, mustParseTools(t, "query_memory"))

			body, _ := json.Marshal(toolenvelope.QueryMemoryRequest{Query: "x"})
			req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/query_memory", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+goodToken)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)
			if w.Code != c.want {
				t.Errorf("status = %d, want %d; body = %s", w.Code, c.want, w.Body.String())
			}
		})
	}
}

func TestQueryMemory_NilQuerierIsServiceUnavailable(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "query_memory"))

	body, _ := json.Marshal(toolenvelope.QueryMemoryRequest{Query: "x"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/query_memory", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}
