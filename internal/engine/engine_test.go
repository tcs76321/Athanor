package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/control"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
	"github.com/tcs76321/athanor/migrations"
)

// countingOllama counts chat calls so tests can assert how many LLM
// requests each path performs.
//
// M3-T1: the fake now reads the request's `messages` to discover
// which role (model) it was asked to play, and returns a content
// string appropriate for that phase. The security persona's calls
// (evaluating + comparing) get a JSON verdict the engine can parse;
// every other persona gets the M1-style prose. This keeps existing
// tests honest while letting the M3-T1 phases (which demand JSON
// output) complete.
type countingOllama struct {
	*httptest.Server
	mu                 sync.Mutex
	calls              int
	callsByPhase       map[string]int
	evalVerdicts       []string
	comparisonVerdicts []string
	// lastPrompt is the concatenated message content of the most recent
	// chat request (M5-T5: tests assert on the assembled prompt — the
	// active chunk, the Dormant Index, the tool manifest).
	lastPrompt string
	// delay is the artificial latency the fake introduces per
	// request. Tests for the per-phase wall-time budget
	// (M3-T2 commit 2.4) set this to a value larger than the
	// budget to exercise the deadline path. Default 0.
	delay time.Duration
	// planningContent, when non-empty, is the response the fake returns
	// for the planning phase. F4-T2 tests use it to emit a
	// `DIFFICULTY: easy|hard` hint the engine parses.
	planningContent string
}

func newCountingOllama(t *testing.T) *countingOllama {
	t.Helper()
	o := &countingOllama{callsByPhase: map[string]int{}}
	o.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			_, _ = w.Write([]byte(`{"version":"fake"}`))
			return
		}
		// Read the body fully (not via Decoder) to keep the handler
		// independent of how the client closes the request stream;
		// the LLM client sends a Content-Length body that the
		// server can read in one go.
		bodyBytes, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(bodyBytes, &req)

		o.mu.Lock()
		o.calls++
		// Heuristic: the phase is the first occurrence of "PHASE: X"
		// in the system+user messages. The prompt assembly writes
		// the runtime-policy section ("PHASE: EVALUATING …") first.
		phase := "unknown"
		for _, m := range req.Messages {
			if idx := strings.Index(m.Content, "PHASE: "); idx >= 0 {
				rest := m.Content[idx+len("PHASE: "):]
				for i, c := range rest {
					if c == '\n' || c == '.' || c == ' ' {
						phase = rest[:i]
						break
					}
				}
				break
			}
		}
		o.callsByPhase[phase]++
		var sb strings.Builder
		for _, m := range req.Messages {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
		o.lastPrompt = sb.String()
		_ = req

		content := "A thoughtful result."
		if phase == "PLANNING" && o.planningContent != "" {
			content = o.planningContent
		}
		// M3-T2 commit 2.4: sleep for the configured delay
		// after reading the request body, but only if the
		// request's context is still alive. (The client's
		// http.NewRequestWithContext cancellation propagates
		// through http.Do, but the handler is the one that
		// returns to the wire; we need to keep the response
		// short so the client sees a context-deadline error
		// rather than a closed-connection error.)
		if o.delay > 0 {
			delay := o.delay
			// Honor the request's context: if the client
			// canceled, return immediately so the
			// response-body read on the client side is a
			// network error, not a 200 with a wrong body.
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		// Security persona is used by evaluating + comparing. Both
		// demand a structured JSON verdict.
		if isSecurityModel(req.Model) || phase == "EVALUATION" || phase == "COMPARISON" {
			switch phase {
			case "EVALUATION":
				if len(o.evalVerdicts) > 0 {
					content = o.evalVerdicts[0]
					o.evalVerdicts = o.evalVerdicts[1:]
				} else {
					content = `{"passed":true,"score":0.9,"failed_tests":[],"missing_criteria":[],"security_issues":[],"style_issues":[],"better_than_previous":true,"confidence":0.9,"summary":"candidate meets all criteria"}`
				}
			case "COMPARISON":
				if len(o.comparisonVerdicts) > 0 {
					content = o.comparisonVerdicts[0]
					o.comparisonVerdicts = o.comparisonVerdicts[1:]
				} else {
					content = `{"winner":"new","confidence":0.9,"reasons":["new candidate passes all criteria"],"missing_requirements":[]}`
				}
			}
		}
		o.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": content},
			"done":              true,
			"prompt_eval_count": 10,
			"eval_count":        5,
		})
	}))
	t.Cleanup(o.Close)
	return o
}

// isSecurityModel is a tiny heuristic for the test fake: the
// `security` persona is wired to a model name that contains
// "security" in the test registry, OR is empty (the default test
// registry uses an empty string and the engine infers security from
// the phase). For the fake's purposes, anything the M1 tests called
// "main" is fine to return prose; only evaluating+comparing demand
// JSON. The phase detection above is the actual trigger.
func isSecurityModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "security")
}

// lastPromptText returns the most recent request's concatenated message
// content (mutex-guarded; the fake serves concurrent jobs).
func (o *countingOllama) lastPromptText() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastPrompt
}

// callCount returns the number of chat requests served.
func (o *countingOllama) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

// WithDelay configures the fake to sleep `d` per request. Tests
// for the per-phase wall-time budget (M3-T2 commit 2.4) use
// this to simulate a slow Ollama and exercise the
// context-deadline path.
func (o *countingOllama) WithDelay(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.delay = d
}

// setPlanning configures the fake's planning-phase response (F4-T2 tests
// use it to emit a `DIFFICULTY:` hint).
func (o *countingOllama) setPlanning(content string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.planningContent = content
}

type testEnv struct {
	cfg       *config.Config
	db        *store.Store
	jobs      *job.Repository
	projects  *project.Repo
	artifacts *artifact.Store
	freezer   *control.KillSwitch
	ollama    *countingOllama
	eng       *Engine
	runner    *fakeRunner
}

func newEnv(t *testing.T) *testEnv {
	return newEnvWithCfg(t, nil)
}

// newEnvWithCfg lets a test mutate the config before the engine (and its
// persona registry snapshot) is built.
func newEnvWithCfg(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	// M3-T1: the evaluating and comparing phases use the `security`
	// persona, whose default `ContextTarget` is 8192 — too small for
	// the default 32768 coding floor. Bump it so the §12.6 feasibility
	// check passes for every archetype in the test fixtures. The
	// test fake doesn't care about the actual context size; this is
	// purely a feasibility arithmetic fix.
	cfg.Personas.Security.ContextTarget = cfg.ContextEngine.CodingFloor
	if cfg.Personas.Security.ContextTarget < cfg.ContextEngine.DocumentFloor {
		cfg.Personas.Security.ContextTarget = cfg.ContextEngine.DocumentFloor
	}
	ollama := newCountingOllama(t)
	cfg.Inference.OllamaURL = ollama.URL
	if mutate != nil {
		mutate(cfg)
	}

	freezer, err := control.NewKillSwitch(db)
	if err != nil {
		t.Fatal(err)
	}
	projects := project.NewRepo(db)
	jobs := job.NewRepository(db)
	artifacts := artifact.NewStore(db, filepath.Join(dir, "artifacts"))
	registry, err := llm.NewRegistry(cfg.Personas)
	if err != nil {
		t.Fatal(err)
	}
	runner := newFakeRunner()
	eng := New(cfg, db, jobs, projects, artifacts, evaluation.NewRepo(db), llm.NewClient(cfg.Inference.OllamaURL, nil), registry, freezer, power.NewPowerManager(nil), runner, nil, nil, nil)
	return &testEnv{cfg: cfg, db: db, jobs: jobs, projects: projects, artifacts: artifacts,
		freezer: freezer, ollama: ollama, eng: eng, runner: runner}
}

// submit creates a project with a text goal and its queued job. Names are
// unique per call (the projects.name column is UNIQUE).
var submitSeq int

func (e *testEnv) submit(t *testing.T) (jobID string) {
	t.Helper()
	return e.submitArchetype(t, "text", "Write a short essay about local-first software.")
}

// submitCode is the M2-T4 counterpart: a code-archetype project
// that the engine will route through runCodeInPod and
// runTestsInPod. The goal text satisfies the project's
// goalMinLen (20 chars).
func (e *testEnv) submitCode(t *testing.T) (jobID string) {
	t.Helper()
	return e.submitArchetype(t, "code", "Write a Python module that manages a personal book collection with add, list, and search functions.")
}

// submitArchetype is the shared implementation; submit and
// submitCode are thin wrappers that pin the two archetypes the
// engine tests actually care about.
func (e *testEnv) submitArchetype(t *testing.T, archetype, goal string) (jobID string) {
	t.Helper()
	_, _, jobID = e.createProjectTask(t, archetype, goal)
	return jobID
}

// createProjectTask creates one project + task + job and returns
// all three IDs. M3-T1 tests need the project and task IDs
// (e.g. to seed a previous accepted artifact) which the slim
// submit/submitCode helpers don't expose.
func (e *testEnv) createProjectTask(t *testing.T, archetype, goal string) (projectID, taskID, jobID string) {
	t.Helper()
	submitSeq++
	p, task, err := e.projects.Create(context.Background(),
		fmt.Sprintf("demo-%s-%d", archetype, submitSeq), archetype, goal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.jobs.Create(context.Background(), task.ID, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID, task.ID, j.ID
}

// TestRunCompletesFullChain drives a job synchronously to completion.
func TestRunCompletesFullChain(t *testing.T) {
	e := newEnv(t)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed", j.State)
	}
	// M3-T1 dialectical loop: planning + 3×diverging + 3×evaluating
	// + synthesizing + comparing. The default divergence candidate
	// count is 3; evaluating scales 1:1 with candidates; comparing
	// asks the security persona for the §19.3 verdict (1 call).
	// Total = 1 + 3 + 3 + 1 + 1 = 9.
	if e.ollama.calls != 9 {
		t.Errorf("llm calls = %d, want 9 (M3-T1: plan + 3*div + 3*eval + synth + compare)", e.ollama.calls)
	}
	// Both artifacts exist.
	if _, err := e.artifacts.LatestForJob(context.Background(), jobID, artifact.KindProposal); err != nil {
		t.Errorf("proposal artifact missing: %v", err)
	}
	if _, err := e.artifacts.LatestForJob(context.Background(), jobID, artifact.KindDocument); err != nil {
		t.Errorf("final artifact missing: %v", err)
	}
}

// TestOnJobTerminalFiresForCompletion proves the M6-T2 terminal seam
// (ADR-0033): the engine notifies a wired observer exactly once when a job
// reaches a terminal state, with the job's final state.
func TestOnJobTerminalFiresForCompletion(t *testing.T) {
	e := newEnv(t)
	jobID := e.submit(t)
	type signal struct {
		id    string
		state job.State
	}
	var got []signal
	e.eng.SetOnJobTerminal(func(_ context.Context, id string, s job.State) {
		got = append(got, signal{id, s})
	})
	e.eng.Run(context.Background(), jobID)
	if len(got) != 1 || got[0].id != jobID || got[0].state != job.StateCompleted {
		t.Fatalf("terminal callback = %+v, want one completed for %s", got, jobID)
	}
}

type fakeCorrectionSink struct {
	calls int
	in    corrections.CaptureInput
}

func (f *fakeCorrectionSink) Capture(_ context.Context, in corrections.CaptureInput) (corrections.Record, error) {
	f.calls++
	f.in = in
	return corrections.Record{ID: "c1"}, nil
}

// TestPhaseFailureRecordsCorrection proves the M6-T6 engine seam: a phase
// failure is reported to the correction sink as a runtime_error source.
func TestPhaseFailureRecordsCorrection(t *testing.T) {
	e := newEnvWithCfg(t, func(c *config.Config) {
		// An unreachable Ollama makes the first phase call fail.
		c.Inference.OllamaURL = "http://127.0.0.1:1"
	})
	sink := &fakeCorrectionSink{}
	e.eng.SetCorrectionSink(sink)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	if sink.calls != 1 {
		t.Fatalf("correction captures = %d, want 1", sink.calls)
	}
	if sink.in.Source != corrections.SourceRuntimeError || sink.in.JobID != jobID {
		t.Errorf("capture input = %+v, want runtime_error for %s", sink.in, jobID)
	}
}

type fakeCorrectionSource struct {
	recs    []corrections.Record
	applied []string
}

func (f *fakeCorrectionSource) Relevant(_ context.Context, _ string, _ int) ([]corrections.Record, error) {
	return f.recs, nil
}

func (f *fakeCorrectionSource) MarkApplied(_ context.Context, id string) error {
	f.applied = append(f.applied, id)
	return nil
}

// TestCorrectionsInjectedAndAudited proves the M6-T7 seam: active
// corrections reach the prompt, are audited with token accounting, and
// increment their applied count.
func TestCorrectionsInjectedAndAudited(t *testing.T) {
	e := newEnv(t)
	src := &fakeCorrectionSource{recs: []corrections.Record{
		{ID: "c1", Category: "style", Severity: "high", Scope: "project", DerivedRule: "prefer dependency injection"},
	}}
	e.eng.SetCorrectionSource(src)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	if len(src.applied) == 0 {
		t.Fatal("injected correction was never marked applied")
	}
	events, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID, Category: "feedback"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if strings.Contains(ev.DataJSON, "corrections_injected") && strings.Contains(ev.DataJSON, "c1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no corrections_injected audit row: %v", events)
	}
}

type fakeInterruptionStore struct {
	notes    []interruptions.Note
	injected []string
}

func (f *fakeInterruptionStore) Pending(_ context.Context, _ string) ([]interruptions.Note, error) {
	return f.notes, nil
}

func (f *fakeInterruptionStore) MarkInjected(_ context.Context, ids []string) error {
	f.injected = append(f.injected, ids...)
	return nil
}

// TestInterruptionInjectedAndMarked proves the M6-T8c §20.4 seam: a queued
// note is injected at a safe point and marked injected with an audit row.
func TestInterruptionInjectedAndMarked(t *testing.T) {
	e := newEnv(t)
	sink := &fakeInterruptionStore{notes: []interruptions.Note{{ID: "n1", Text: "be brief"}}}
	e.eng.SetInterruptionStore(sink)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	if len(sink.injected) == 0 {
		t.Fatal("interruption note was never marked injected")
	}
	events, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if strings.Contains(ev.DataJSON, "interruption_injected") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no interruption_injected audit row: %v", events)
	}
}

// TestStrategyProfileAndOutcomeCaptured proves the M6-T10 §13.3 seam: a
// completed job has both a profile (built from the persona plan) and an
// outcome linked to it.
func TestStrategyProfileAndOutcomeCaptured(t *testing.T) {
	e := newEnv(t)
	sink := strategy.NewRepo(e.db)
	e.eng.SetStrategySink(sink)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	profile, err := sink.GetProfileByJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(profile.Signature) != 6 {
		t.Errorf("signature entries = %d, want 6", len(profile.Signature))
	}
	outcome, err := sink.GetOutcomeByJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("outcome: %v", err)
	}
	if !strategy.ValidResult(outcome.Result) {
		t.Errorf("result = %q", outcome.Result)
	}
	if outcome.StrategyProfileID != profile.ID {
		t.Errorf("outcome profile = %q, want %q", outcome.StrategyProfileID, profile.ID)
	}
}

// TestStrategyOutcomeOnFailure proves a failed job still produces an outcome.
func TestStrategyOutcomeOnFailure(t *testing.T) {
	e := newEnvWithCfg(t, func(c *config.Config) {
		c.Inference.OllamaURL = "http://127.0.0.1:1"
	})
	sink := strategy.NewRepo(e.db)
	e.eng.SetStrategySink(sink)
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	outcome, err := sink.GetOutcomeByJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("outcome: %v", err)
	}
	if outcome.Result != strategy.ResultFailed {
		t.Errorf("result = %q, want failed", outcome.Result)
	}
}

// TestProposedInsightDoesNotAffectPrompt proves the M6-T11 acceptance bar: a
// *proposed* strategy insight is inert (never in a prompt); promoting it to
// active makes it appear.
func TestProposedInsightDoesNotAffectPrompt(t *testing.T) {
	e := newEnv(t)
	repo := strategy.NewRepo(e.db)
	e.eng.SetStrategyInsightSource(repo)

	ins, err := repo.CreateInsight(context.Background(), strategy.Insight{
		Scope: strategy.ScopeGlobal, Polarity: strategy.PolarityWinning,
		Pattern:   strategy.Pattern{Feature: "diverging.persona", Value: "alternative", Context: "archetype=code"},
		Statement: "UNIQUE_MARKER prefer alternative divergence", Status: strategy.InsightProposed,
	})
	if err != nil {
		t.Fatal(err)
	}

	job1 := e.submit(t)
	e.eng.Run(context.Background(), job1)
	if strings.Contains(e.ollama.lastPrompt, "UNIQUE_MARKER") {
		t.Fatal("proposed insight leaked into a prompt")
	}

	if err := repo.SetInsightStatus(context.Background(), ins.ID, strategy.InsightActive); err != nil {
		t.Fatal(err)
	}
	job2 := e.submit(t)
	e.eng.Run(context.Background(), job2)
	if !strings.Contains(e.ollama.lastPrompt, "UNIQUE_MARKER") {
		t.Fatal("active insight missing from the prompt")
	}
}

// fixedCap is a ConcurrencyCap that returns a fixed value, used by
// M1-T8.4 tests to drive the engine's concurrency behavior.
type fixedCap struct{ n int }

func (f fixedCap) MaxConcurrentJobs() int { return f.n }

// TestEnqueueRespectsConcurrencyCap (M1-T8.4) proves the engine reads
// its concurrency cap from the injected ConcurrencyCap on every
// enqueue, and that a cap of 1 limits in-flight job goroutines to 1.
// We use a fake Ollama that blocks on a channel, so we can hold two
// jobs in flight simultaneously and observe the cap block the second.
func TestEnqueueRespectsConcurrencyCap(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}

	// A blocking fake ollama: each call parks on a release channel
	// so we can hold N jobs in flight at once. M3-T1: the fake
	// also returns a JSON verdict for evaluating/comparing calls
	// (the security persona's output) so the dialectical loop
	// can parse the response without panicking. The blocking
	// release happens AFTER the verdict is selected, so a release
	// during evaluating still returns the JSON the engine expects.
	released := make(chan struct{})
	hang := make(chan struct{})
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			_, _ = w.Write([]byte(`{"version":"fake"}`))
			return
		}
		// Determine the phase by reading the request body.
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		phase := "unknown"
		for _, m := range req.Messages {
			if idx := strings.Index(m.Content, "PHASE: "); idx >= 0 {
				rest := m.Content[idx+len("PHASE: "):]
				for i, c := range rest {
					if c == '\n' || c == '.' || c == ' ' {
						phase = rest[:i]
						break
					}
				}
				break
			}
		}
		content := "x"
		switch phase {
		case "EVALUATION":
			content = `{"passed":true,"score":0.9,"failed_tests":[],"missing_criteria":[],"security_issues":[],"style_issues":[],"better_than_previous":true,"confidence":0.9,"summary":"ok"}`
		case "COMPARISON":
			content = `{"winner":"new","confidence":0.9,"reasons":["ok"],"missing_requirements":[]}`
		}
		<-hang
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": content},
			"done":              true,
			"prompt_eval_count": 1, "eval_count": 1,
		})
	}))
	t.Cleanup(ollama.Close)
	cfg.Inference.OllamaURL = ollama.URL

	freezer, _ := control.NewKillSwitch(db)
	registry, _ := llm.NewRegistry(cfg.Personas)
	artifacts := artifact.NewStore(db, filepath.Join(dir, "artifacts"))

	// Cap = 1: the engine must hold at most one in-flight job goroutine.
	cap := fixedCap{n: 1}
	eng := New(cfg, db, job.NewRepository(db), project.NewRepo(db), artifacts,
		evaluation.NewRepo(db),
		llm.NewClient(cfg.Inference.OllamaURL, nil), registry, freezer, cap, newFakeRunner(), nil, nil, nil)

	// Submit two jobs. The first enters the LLM call (blocked on `hang`).
	// The second must wait in Enqueue's poll loop.
	projects := project.NewRepo(db)
	_, task1, err := projects.Create(context.Background(), "cap-1", "text",
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	j1, err := job.NewRepository(db).Create(context.Background(), task1.ID, task1.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	_, task2, err := projects.Create(context.Background(), "cap-2", "text",
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := job.NewRepository(db).Create(context.Background(), task2.ID, task2.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	eng.Enqueue(j1.ID)
	eng.Enqueue(j2.ID)

	// Give both goroutines a moment: j1's LLM call is hanging, j2's
	// Enqueue poll-loop is spinning. If the cap is honored, j2's
	// in-flight count is 0 and the LLM was never called for j2.
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt64(&eng.inFlight); got != 1 {
		t.Errorf("inFlight = %d, want 1 (cap=1 must hold a second job out of the run loop)", got)
	}

	// Release the LLM and let j1 finish; j2 should then run.
	close(hang)
	close(released)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j1s, err := job.NewRepository(db).Get(context.Background(), j1.ID)
		if err != nil {
			t.Fatal(err)
		}
		j2s, err := job.NewRepository(db).Get(context.Background(), j2.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j1s.State == job.StateCompleted && j2s.State == job.StateCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("both jobs did not complete within deadline")
}
