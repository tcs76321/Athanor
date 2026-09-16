package internalapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// fakeGateway is a map-backed ToolGateway for the M4-T7 handler
// tests. Each call records the request; behavior is set per job by
// the test (result or error).
type fakeGateway struct {
	fetchReqs  []toolenvelope.FetchURLRequest
	searchReqs []toolenvelope.SearchWebRequest
	fetchRes   *toolenvelope.FetchURLResponse
	fetchErr   error
	searchRes  *toolenvelope.SearchWebResponse
	searchErr  error
}

func (f *fakeGateway) FetchURL(_ context.Context, req toolenvelope.FetchURLRequest) (*toolenvelope.FetchURLResponse, error) {
	f.fetchReqs = append(f.fetchReqs, req)
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.fetchRes, nil
}

func (f *fakeGateway) SearchWeb(_ context.Context, req toolenvelope.SearchWebRequest) (*toolenvelope.SearchWebResponse, error) {
	f.searchReqs = append(f.searchReqs, req)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchRes, nil
}

// newHandlerTestEnvWithGateway is newHandlerTestEnv with a ToolGateway
// wired (the M4-T7.4 production shape).
func newHandlerTestEnvWithGateway(t *testing.T, gw ToolGateway) *handlerTestEnv {
	t.Helper()
	env := newHandlerTestEnv(t)
	env.api.gateway = gw
	return env
}

// countToolEvents counts jobs-category events with the given event
// name and tool for the job.
func countToolEvents(t *testing.T, env *handlerTestEnv, jobID, event, tool string) int {
	t.Helper()
	events, err := env.store.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		var d struct {
			Event string `json:"event"`
			Tool  string `json:"tool"`
		}
		_ = json.Unmarshal([]byte(e.DataJSON), &d)
		if d.Event == event && d.Tool == tool {
			n++
		}
	}
	return n
}

// --- fetch_url ----------------------------------------------------------

func TestFetchURL_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/fetch_url",
		bytes.NewReader([]byte(`{"url":"https://example.org/"}`)))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no bearer)", w.Code)
	}
}

func TestFetchURL_RejectsWhenToolNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnvWithGateway(t, &fakeGateway{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	body, _ := json.Marshal(toolenvelope.FetchURLRequest{URL: "https://example.org/"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/fetch_url", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(ErrToolDisallowed.Error())) {
		t.Errorf("body = %q, want ErrToolDisallowed text", w.Body.String())
	}
	if got := countToolEvents(t, env, taskID, "tool_disallowed", "fetch_url"); got != 1 {
		t.Errorf("tool_disallowed events = %d, want 1", got)
	}
}

func TestFetchURL_BadInput(t *testing.T) {
	env := newHandlerTestEnvWithGateway(t, &fakeGateway{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "fetch_url"))

	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty url", `{}`, http.StatusBadRequest},
		{"bad scheme", `{"url":"ftp://example.org/"}`, http.StatusBadRequest},
		{"no host", `{"url":"/only/a/path"}`, http.StatusBadRequest},
		{"unparsable", `{"url":"ht tp://["}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/fetch_url",
				bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Authorization", "Bearer "+goodToken)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestFetchURL_NilGatewayReturns503(t *testing.T) {
	env := newHandlerTestEnv(t) // gateway nil
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "fetch_url"))

	body, _ := json.Marshal(toolenvelope.FetchURLRequest{URL: "https://example.org/"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/fetch_url", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}

func TestFetchURL_HappyPath(t *testing.T) {
	gw := &fakeGateway{fetchRes: &toolenvelope.FetchURLResponse{
		StatusCode: 200, URL: "https://example.org/", Markdown: "# hello", Mode: "readability",
	}}
	env := newHandlerTestEnvWithGateway(t, gw)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "fetch_url"))

	body, _ := json.Marshal(toolenvelope.FetchURLRequest{URL: "https://example.org/", TimeoutSeconds: 5})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/fetch_url", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var res toolenvelope.FetchURLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Markdown != "# hello" || res.Mode != "readability" {
		t.Errorf("response = %+v, want markdown + readability mode", res)
	}
	if len(gw.fetchReqs) != 1 || gw.fetchReqs[0].URL != "https://example.org/" || gw.fetchReqs[0].Tool != toolenvelope.ToolFetchURL {
		t.Errorf("gateway got %+v, want one request with the URL and Tool set", gw.fetchReqs)
	}
	if got := countToolEvents(t, env, taskID, "tool_call", "fetch_url"); got != 1 {
		t.Errorf("tool_call events = %d, want 1", got)
	}
}

func TestFetchURL_GatewayErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"denied", fmt.Errorf("wrapped: %w", ErrFetchDenied), http.StatusForbidden},
		{"content rejected", fmt.Errorf("wrapped: %w", ErrContentRejected), http.StatusUnprocessableEntity},
		{"failed", fmt.Errorf("dial: %w", ErrFetchFailed), http.StatusBadGateway},
		{"unknown", errors.New("mystery"), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newHandlerTestEnvWithGateway(t, &fakeGateway{fetchErr: tc.err})
			_, taskID := env.seedProject(t)
			env.tokens.WithToken(taskID, goodToken)
			env.tools.WithAllow(taskID, mustParseTools(t, "fetch_url"))

			body, _ := json.Marshal(toolenvelope.FetchURLRequest{URL: "https://example.org/"})
			req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/fetch_url", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+goodToken)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// --- search_web ---------------------------------------------------------

func TestSearchWeb_RejectsWhenToolNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnvWithGateway(t, &fakeGateway{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)

	body, _ := json.Marshal(toolenvelope.SearchWebRequest{Query: "local-first agents"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/search_web", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if got := countToolEvents(t, env, taskID, "tool_disallowed", "search_web"); got != 1 {
		t.Errorf("tool_disallowed events = %d, want 1", got)
	}
}

func TestSearchWeb_BadInput(t *testing.T) {
	env := newHandlerTestEnvWithGateway(t, &fakeGateway{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "search_web"))

	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty query", `{}`, http.StatusBadRequest},
		{"whitespace query", `{"query":"   "}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/search_web",
				bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Authorization", "Bearer "+goodToken)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestSearchWeb_NilGatewayReturns503(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "search_web"))

	body, _ := json.Marshal(toolenvelope.SearchWebRequest{Query: "q"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/search_web", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}

func TestSearchWeb_HappyPath(t *testing.T) {
	gw := &fakeGateway{searchRes: &toolenvelope.SearchWebResponse{
		Query: "local-first agents",
		Results: []toolenvelope.SearchResult{
			{Title: "Local-first software", URL: "https://example.org/a", Snippet: "..."},
		},
	}}
	env := newHandlerTestEnvWithGateway(t, gw)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "search_web"))

	body, _ := json.Marshal(toolenvelope.SearchWebRequest{Query: "local-first agents"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/search_web", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var res toolenvelope.SearchWebResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].URL != "https://example.org/a" {
		t.Errorf("response = %+v, want one result", res)
	}
	if len(gw.searchReqs) != 1 || gw.searchReqs[0].Tool != toolenvelope.ToolSearchWeb {
		t.Errorf("gateway got %+v, want one request with Tool set", gw.searchReqs)
	}
	if got := countToolEvents(t, env, taskID, "tool_call", "search_web"); got != 1 {
		t.Errorf("tool_call events = %d, want 1", got)
	}
}

func TestSearchWeb_NotConfiguredReturns501(t *testing.T) {
	env := newHandlerTestEnvWithGateway(t, &fakeGateway{searchErr: ErrSearchNotConfigured})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "search_web"))

	body, _ := json.Marshal(toolenvelope.SearchWebRequest{Query: "q"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/search_web", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("not configured")) {
		t.Errorf("body = %q, want the ErrSearchNotConfigured text", w.Body.String())
	}
}