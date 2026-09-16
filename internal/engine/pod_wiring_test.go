package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// fakeRunner is a ToolRunner impl for tests. It records every
// call and returns canned output. The default canned output
// represents a successful program run; tests that want
// different behavior override it via the WithExit /
// WithError / WithDisallow helpers.
//
// fakeRunner is safe for concurrent use: tests can run jobs
// in parallel and assert on the slice of calls.
type fakeRunner struct {
	mu       sync.Mutex
	calls    []fakeCall
	exitCode int
	duration time.Duration
	withErr  error
	disallow map[string]bool
	// M4-T7 research sub-step knobs (FetchURL): canned markdown per
	// URL, plus a global failure override.
	fetches  map[string]string
	fetchErr error
}

type fakeCall struct {
	Tool  string
	JobID string
	Lang  string
	Code  string
	Cmd   string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		exitCode: 0,
		duration: 10 * time.Millisecond,
		disallow: map[string]bool{},
		fetches:  map[string]string{},
	}
}

// WithFetch seeds a canned markdown for a URL (FetchURL).
func (f *fakeRunner) WithFetch(url, markdown string) *fakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches[url] = markdown
	return f
}

// WithFetchErr makes every FetchURL fail with err.
func (f *fakeRunner) WithFetchErr(err error) *fakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchErr = err
	return f
}

func (f *fakeRunner) WithDisallow(tool toolenvelope.Tool) *fakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disallow[string(tool)] = true
	return f
}

func (f *fakeRunner) WithExit(code int) *fakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exitCode = code
	return f
}

func (f *fakeRunner) WithError(err error) *fakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withErr = err
	return f
}

func (f *fakeRunner) Calls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeRunner) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRunner) RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return f.run(ctx, jobID, req, "RunCode")
}

func (f *fakeRunner) RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return f.run(ctx, jobID, req, "RunTests")
}

// fetches holds the canned fetch outcomes (M4-T7 research sub-step).
// The key is the requested URL; a missing key means "success with
// the canned markdown". fetchErr / fetchDisallow override globally.
func (f *fakeRunner) FetchURL(_ context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{Tool: "FetchURL", JobID: jobID, Cmd: req.URL})
	if f.fetchErr != nil {
		return toolenvelope.FetchURLResponse{}, f.fetchErr
	}
	if f.disallow["FetchURL"] {
		return toolenvelope.FetchURLResponse{}, toolenvelope.ErrToolDisallowed
	}
	md, ok := f.fetches[req.URL]
	if !ok {
		md = "fake source markdown for " + req.URL
	}
	return toolenvelope.FetchURLResponse{
		StatusCode: 200, URL: req.URL, Markdown: md, Mode: "readability",
	}, nil
}

func (f *fakeRunner) run(_ context.Context, jobID string, req toolenvelope.ExecuteRequest, kind string) (toolenvelope.ExecuteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{
		Tool:  kind,
		JobID: jobID,
		Lang:  req.Language,
		Code:  req.Code,
		Cmd:   req.Command,
	})
	if f.withErr != nil {
		return toolenvelope.ExecuteResult{}, f.withErr
	}
	if f.disallow[kind] {
		return toolenvelope.ExecuteResult{}, toolenvelope.ErrToolDisallowed
	}
	return toolenvelope.ExecuteResult{
		ExitCode:   f.exitCode,
		Stdout:     "fake stdout",
		Stderr:     "",
		DurationMS: f.duration.Milliseconds(),
	}, nil
}

// TestRun_CodeArchetypeCallsRunner is the M2-T4/M3-T2 behavioral
// proof that the engine calls the ToolRunner for code-archetype
// jobs. The text-archetype test (TestRun_TextArchetypeDoesNotCallRunner,
// below) asserts the inverse: 0 runner calls.
//
// M3-T2 (ADR-0014) moved both RunCode and RunTests from the
// synthesis phase into the per-candidate `evaluating` phase.
// After Run completes, the log has 3 RunCode calls + 3
// RunTests calls, all in `evaluating`. `synthesizing` no longer
// touches the runner.
func TestRun_CodeArchetypeCallsRunner(t *testing.T) {
	env := newEnv(t)
	codeJobID := env.submitCode(t)

	env.eng.Run(context.Background(), codeJobID)

	// M3-T2 (ADR-0014): 3 candidates × (RunCode + RunTests) in
	// `evaluating` = 6 runner calls. `synthesizing` makes zero
	// runner calls.
	if got := env.runner.CallCount(); got != 6 {
		t.Errorf("runner calls = %d, want 6 (M3-T2: 3*evaluating RunCode + 3*evaluating RunTests)", got)
	}
	calls := env.runner.Calls()
	if len(calls) != 6 {
		t.Fatalf("calls = %d, want 6", len(calls))
	}
	codeCount, testCount := 0, 0
	var codeCall, testCall fakeCall
	for _, c := range calls {
		switch c.Tool {
		case "RunCode":
			codeCount++
			codeCall = c
		case "RunTests":
			testCount++
			testCall = c
		}
	}
	if codeCount != 3 {
		t.Errorf("RunCode calls = %d, want 3 (one per candidate, all in evaluating)", codeCount)
	}
	if codeCall.Lang != "python" {
		t.Errorf("RunCode language = %q, want python", codeCall.Lang)
	}
	if testCount != 3 {
		t.Errorf("RunTests calls = %d, want 3 (one per candidate, all in evaluating)", testCount)
	}
	if testCall.Cmd != "pytest -q" {
		t.Errorf("RunTests command = %q, want pytest -q", testCall.Cmd)
	}
}

// TestRun_TextArchetypeDoesNotCallRunner is the inverse: text
// archetype skips the runner sub-steps entirely. The M1 walking
// skeleton still produces 3 LLM calls and 0 runner calls.
func TestRun_TextArchetypeDoesNotCallRunner(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)
	env.eng.Run(context.Background(), jobID)
	if got := env.runner.CallCount(); got != 0 {
		t.Errorf("runner calls = %d, want 0 for text archetype", got)
	}
}

// TestRun_ToolDisallowedSoftFails asserts the soft-fail
// behavior: when the runner returns toolenvelope.ErrToolDisallowed,
// the engine continues to comparing and the job completes.
// M3-T2 will turn this into a HITL escalation.
func TestRun_ToolDisallowedSoftFails(t *testing.T) {
	env := newEnv(t)
	jobID := env.submitCode(t)
	env.runner.WithDisallow(toolenvelope.ToolExecuteCode)
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Errorf("state = %s, want completed (disallow is a soft-fail)", j.State)
	}
	if env.runner.CallCount() < 1 {
		t.Errorf("runner calls = %d, want >= 1 (at least RunCode before the disallow)", env.runner.CallCount())
	}
}
