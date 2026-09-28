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

// fakeSwapper is a ContextSwapper for the M5-T3 handler tests.
type fakeSwapper struct {
	reqs []toolenvelope.ContextSwapRequest
	res  *toolenvelope.ContextSwapResponse
	err  error
}

func (f *fakeSwapper) SwapContext(_ context.Context, req toolenvelope.ContextSwapRequest) (*toolenvelope.ContextSwapResponse, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.res, nil
}

func newHandlerTestEnvWithSwapper(t *testing.T, s ContextSwapper) *handlerTestEnv {
	t.Helper()
	env := newHandlerTestEnv(t)
	env.api.swapper = s
	return env
}

func TestContextSwap_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/context_swap",
		bytes.NewReader([]byte(`{"target_chunk_id":"abc"}`)))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no bearer)", w.Code)
	}
}

func TestContextSwap_RejectsWhenToolNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnvWithSwapper(t, &fakeSwapper{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	body, _ := json.Marshal(toolenvelope.ContextSwapRequest{TargetChunkID: "abc"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/context_swap", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(ErrToolDisallowed.Error())) {
		t.Errorf("body = %q, want ErrToolDisallowed text", w.Body.String())
	}
	if got := countToolEvents(t, env, taskID, "tool_disallowed", "context_swap"); got != 1 {
		t.Errorf("tool_disallowed events = %d, want 1", got)
	}
}

func TestContextSwap_HappyPath(t *testing.T) {
	sw := &fakeSwapper{res: &toolenvelope.ContextSwapResponse{
		Scope: "ignored", LoadedChunkID: "c1", LoadedBytes: 3, LoadedContent: "abc", FlushedChunkID: "c0",
	}}
	env := newHandlerTestEnvWithSwapper(t, sw)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "context_swap"))

	body, _ := json.Marshal(toolenvelope.ContextSwapRequest{TargetChunkID: "c1"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/context_swap", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(sw.reqs) != 1 {
		t.Fatalf("swapper calls = %d, want 1", len(sw.reqs))
	}
	if sw.reqs[0].Tool != toolenvelope.ToolContextSwap {
		t.Errorf("tool = %q, want context_swap", sw.reqs[0].Tool)
	}
	if sw.reqs[0].Scope != taskID {
		t.Errorf("scope = %q, want the authenticated job id %q", sw.reqs[0].Scope, taskID)
	}
	if got := countToolEvents(t, env, taskID, "tool_call", "context_swap"); got != 1 {
		t.Errorf("tool_call events = %d, want 1", got)
	}
	var res toolenvelope.ContextSwapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.LoadedContent != "abc" || res.FlushedChunkID != "c0" {
		t.Errorf("response = %+v", res)
	}
}

func TestContextSwap_RejectsMissingTarget(t *testing.T) {
	env := newHandlerTestEnvWithSwapper(t, &fakeSwapper{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "context_swap"))

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/context_swap",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func TestContextSwap_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"chunk not found", ErrChunkNotFound, http.StatusNotFound},
		{"swapping disabled", ErrSwapDisabled, http.StatusNotImplemented},
		{"unexpected failure", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newHandlerTestEnvWithSwapper(t, &fakeSwapper{err: c.err})
			_, taskID := env.seedProject(t)
			env.tokens.WithToken(taskID, goodToken)
			env.tools.WithAllow(taskID, mustParseTools(t, "context_swap"))

			body, _ := json.Marshal(toolenvelope.ContextSwapRequest{TargetChunkID: "c1"})
			req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/context_swap", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+goodToken)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)
			if w.Code != c.want {
				t.Errorf("status = %d, want %d; body = %s", w.Code, c.want, w.Body.String())
			}
		})
	}
}

func TestContextSwap_NilSwapperIsServiceUnavailable(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "context_swap"))

	body, _ := json.Marshal(toolenvelope.ContextSwapRequest{TargetChunkID: "c1"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/context_swap", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}
