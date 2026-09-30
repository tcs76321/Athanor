package internalapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// mustParseTools is a tiny test helper that fails the test if
// the named tool names are not in the closed set. Keeps the
// test bodies free of error-handling noise.
func mustParseTools(t *testing.T, names ...string) toolenvelope.Envelope {
	t.Helper()
	env, err := toolenvelope.Parse(names)
	if err != nil {
		t.Fatalf("toolenvelope.Parse(%v): %v", names, err)
	}
	return env
}

// fakePodExecutor is a PodExecutor that records the requests it received
// and returns a canned result or error. It is the test double for the
// M2-T4b dispatch path.
type fakePodExecutor struct {
	mu       sync.Mutex
	codeReqs []toolenvelope.ExecuteRequest
	testReqs []toolenvelope.ExecuteRequest
	lintReqs []toolenvelope.ExecuteRequest
	result   toolenvelope.ExecuteResult
	err      error
}

func (f *fakePodExecutor) RunCode(_ context.Context, _ string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codeReqs = append(f.codeReqs, req)
	return f.result, f.err
}

func (f *fakePodExecutor) RunTests(_ context.Context, _ string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.testReqs = append(f.testReqs, req)
	return f.result, f.err
}

func (f *fakePodExecutor) Lint(_ context.Context, _ string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lintReqs = append(f.lintReqs, req)
	return f.result, f.err
}

// newHandlerTestEnvWithExecutor is newHandlerTestEnv with a PodExecutor
// wired (the M2-T4b.4 production shape).
func newHandlerTestEnvWithExecutor(t *testing.T, exec PodExecutor) *handlerTestEnv {
	t.Helper()
	env := newHandlerTestEnv(t)
	env.api.exec = exec
	return env
}

// TestExecuteCode_RejectsWithoutToken proves the auth middleware
// still wraps the new route (Gate G2's structural check has a
// matching behavioral test).
func TestExecuteCode_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/execute_code",
		bytes.NewReader([]byte(`{"code":"print(1)"}`)))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no bearer)", w.Code)
	}
}

// TestExecuteCode_RejectsWhenToolNotInEnvelope is the M2-T4
// behavioral proof that the per-job allowlist is enforced: a
// request for a tool the envelope does not include gets 403 and
// an EventLog entry of category "jobs" with event
// "tool_disallowed".
func TestExecuteCode_RejectsWhenToolNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	// Default envelope is empty, so execute_code is disallowed.

	body, _ := json.Marshal(executeCodeRequest{Language: "python", Code: "print(1)"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/execute_code", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(ErrToolDisallowed.Error())) {
		t.Errorf("body = %q, want it to contain ErrToolDisallowed text", w.Body.String())
	}
	events, err := env.store.QueryEvents(context.Background(), store.EventFilter{JobID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	rejections := 0
	for _, e := range events {
		var d struct {
			Event string `json:"event"`
			Tool  string `json:"tool"`
		}
		_ = json.Unmarshal([]byte(e.DataJSON), &d)
		if d.Event == "tool_disallowed" && d.Tool == string(toolenvelope.ToolExecuteCode) {
			rejections++
		}
	}
	if rejections != 1 {
		t.Errorf("tool_disallowed events = %d, want 1", rejections)
	}
}

// TestExecuteCode_DispatchesToExecutor proves the happy path now
// reaches the PodExecutor: auth + envelope + audit all pass, the executor
// runs the code, and its result is returned as 200 JSON. The old 501
// placeholder is gone (M2-T4b).
func TestExecuteCode_DispatchesToExecutor(t *testing.T) {
	fake := &fakePodExecutor{result: toolenvelope.ExecuteResult{ExitCode: 0, Stdout: "1\n", DurationMS: 7}}
	env := newHandlerTestEnvWithExecutor(t, fake)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "execute_code"))

	body, _ := json.Marshal(executeCodeRequest{Language: "python", Code: "print(1)"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/execute_code", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var res toolenvelope.ExecuteResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "1\n" {
		t.Errorf("result = %+v, want exit 0 stdout %q", res, "1\n")
	}
	if len(fake.codeReqs) != 1 {
		t.Fatalf("RunCode calls = %d, want 1", len(fake.codeReqs))
	}
	if got := fake.codeReqs[0]; got.Tool != toolenvelope.ToolExecuteCode ||
		got.Language != "python" || got.Code != "print(1)" {
		t.Errorf("RunCode req = %+v, want tool=execute_code lang=python code=print(1)", got)
	}
	if got := countToolEvents(t, env, taskID, "tool_call", string(toolenvelope.ToolExecuteCode)); got != 1 {
		t.Errorf("tool_call events = %d, want 1", got)
	}
}

// TestExecuteCode_NotConfigured_Returns503 covers the nil-executor
// configuration: the route is registered, auth + envelope pass, and the
// handler reports "not configured" rather than dispatching.
func TestExecuteCode_NotConfigured_Returns503(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "execute_code"))

	body, _ := json.Marshal(executeCodeRequest{Language: "python", Code: "print(1)"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/execute_code", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no executor); body = %s", w.Code, w.Body.String())
	}
}

// TestExecuteCode_ExecutorErrorMapping pins the typed-error → status
// contract of writeExecError, including a wrapped ErrNoPod so the cmd/
// adapter may add context with %w.
func TestExecuteCode_ExecutorErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"no pod", ErrNoPod, http.StatusNotFound},
		{"wrapped no pod", fmt.Errorf("adapter: %w", ErrNoPod), http.StatusNotFound},
		{"not running", ErrPodNotRunning, http.StatusConflict},
		{"unknown", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newHandlerTestEnvWithExecutor(t, &fakePodExecutor{err: tc.err})
			_, taskID := env.seedProject(t)
			env.tokens.WithToken(taskID, goodToken)
			env.tools.WithAllow(taskID, mustParseTools(t, "execute_code"))
			body, _ := json.Marshal(executeCodeRequest{Language: "python", Code: "print(1)"})
			req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/execute_code", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+goodToken)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// TestExecuteCode_RejectsMissingCode covers the 400 path: the
// body is well-formed JSON but lacks the required Code field.
func TestExecuteCode_RejectsMissingCode(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "execute_code"))

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/execute_code",
		bytes.NewReader([]byte(`{"language":"python"}`)))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing code)", w.Code)
	}
}

// TestExecuteCode_RejectsUnknownLanguage covers the closed-set
// check on Language. M2-T4 accepts only "python".
func TestExecuteCode_RejectsUnknownLanguage(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "execute_code"))

	body, _ := json.Marshal(executeCodeRequest{Language: "ruby", Code: "puts 1"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/execute_code", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (language not in closed set)", w.Code)
	}
}

// TestRunTests_RejectsWhenToolNotInEnvelope is the run_tests
// counterpart of TestExecuteCode_RejectsWhenToolNotInEnvelope.
// execute_code in the envelope does not grant run_tests.
func TestRunTests_RejectsWhenToolNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "execute_code"))

	body, _ := json.Marshal(runTestsRequest{Command: "pytest -q"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/run_tests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

// TestRunTests_DispatchesToExecutor is the run_tests counterpart of
// TestExecuteCode_DispatchesToExecutor: the command reaches
// PodExecutor.RunTests and its result is returned as 200 JSON.
func TestRunTests_DispatchesToExecutor(t *testing.T) {
	fake := &fakePodExecutor{result: toolenvelope.ExecuteResult{ExitCode: 1, Stdout: "1 failed"}}
	env := newHandlerTestEnvWithExecutor(t, fake)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "run_tests"))

	body, _ := json.Marshal(runTestsRequest{Command: "pytest -q"})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/run_tests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(fake.testReqs) != 1 {
		t.Fatalf("RunTests calls = %d, want 1", len(fake.testReqs))
	}
	if got := fake.testReqs[0]; got.Tool != toolenvelope.ToolRunTests || got.Command != "pytest -q" {
		t.Errorf("RunTests req = %+v, want tool=run_tests command=pytest -q", got)
	}
}

// TestRunTests_RejectsMissingCommand is the run_tests counterpart
// of TestExecuteCode_RejectsMissingCode.
func TestRunTests_RejectsMissingCommand(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "run_tests"))

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/run_tests",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing command)", w.Code)
	}
}

// TestLint_RejectsWithoutToken proves the auth middleware
// wraps the new /lint route (Gate G2's structural check
// has a matching behavioral test, identical to the
// execute_code / run_tests counterparts).
func TestLint_RejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/lint",
		bytes.NewReader([]byte(`{"command":"ruff check ."}`)))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no bearer)", w.Code)
	}
}

// TestLint_RejectsWhenToolNotInEnvelope is the lint
// counterpart of TestExecuteCode_RejectsWhenToolNotInEnvelope.
// A request for `lint` when the envelope grants only
// `run_tests` is rejected with 403.
func TestLint_RejectsWhenToolNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	// Default envelope is empty, so lint is disallowed.

	body, _ := json.Marshal(lintRequest{Command: "ruff check ."})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/lint", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(ErrToolDisallowed.Error())) {
		t.Errorf("body = %q, want it to contain ErrToolDisallowed text", w.Body.String())
	}
	events, err := env.store.QueryEvents(context.Background(), store.EventFilter{JobID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	rejections := 0
	for _, e := range events {
		var d struct {
			Event string `json:"event"`
			Tool  string `json:"tool"`
		}
		_ = json.Unmarshal([]byte(e.DataJSON), &d)
		if d.Event == "tool_disallowed" && d.Tool == string(toolenvelope.ToolLint) {
			rejections++
		}
	}
	if rejections != 1 {
		t.Errorf("tool_disallowed events = %d, want 1", rejections)
	}
}

// TestLint_DispatchesToExecutor is the lint counterpart: the command
// reaches PodExecutor.Lint and its result is returned as 200 JSON.
func TestLint_DispatchesToExecutor(t *testing.T) {
	fake := &fakePodExecutor{result: toolenvelope.ExecuteResult{ExitCode: 0}}
	env := newHandlerTestEnvWithExecutor(t, fake)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "lint"))

	body, _ := json.Marshal(lintRequest{Command: "ruff check ."})
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/lint", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(fake.lintReqs) != 1 || fake.lintReqs[0].Tool != toolenvelope.ToolLint {
		t.Errorf("Lint reqs = %+v, want one with tool=lint", fake.lintReqs)
	}
}

// TestLint_DefaultCommand fills in `ruff check .` when the request body
// omits `command`; the resolved default reaches the executor, and the
// tool_call audit row is written.
func TestLint_DefaultCommand(t *testing.T) {
	fake := &fakePodExecutor{result: toolenvelope.ExecuteResult{ExitCode: 0}}
	env := newHandlerTestEnvWithExecutor(t, fake)
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, "lint"))

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/lint",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(fake.lintReqs) != 1 || fake.lintReqs[0].Command != allowedLintCommand {
		t.Fatalf("Lint reqs = %+v, want command=%q", fake.lintReqs, allowedLintCommand)
	}
	if got := countToolEvents(t, env, taskID, "tool_call", string(toolenvelope.ToolLint)); got != 1 {
		t.Errorf("tool_call events = %d, want 1", got)
	}
}
