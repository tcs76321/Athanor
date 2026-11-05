package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/store"
)

// eventField returns the value of one decoded field from the first `jobs`
// event whose `event` name matches, plus whether it was found.
func eventField(t *testing.T, e *testEnv, jobID, event, field string) (any, bool) {
	t.Helper()
	events, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		var d map[string]any
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		if d["event"] == event {
			return d[field], true
		}
	}
	return nil, false
}

// TestPhaseCompareVerifiesFullContentNotTruncated is the C1 regression:
// the acceptance-boundary verifier must run against the final artifact's full
// bytes, not ADR-0013's 4 KB judge-prompt head. For a code archetype the
// verifier writes the content to the Job Pod and runs the real test command,
// so a truncated copy would falsely fail any artifact over 4 KB.
func TestPhaseCompareVerifiesFullContentNotTruncated(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.JudgeMode = config.JudgeModeVerifier
	})
	// ~15 KB of valid Python (a comment block) — well over the 4 KB limit.
	big := strings.Repeat("# comment line\n", 1000)
	e.ollama.setGeneric(big)
	jobID := e.submitCode(t)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed (full-content verifier accepted)", j.State)
	}

	// The last RunCode call is the acceptance-boundary verification. It must
	// carry the full artifact, not the truncated comparison head.
	var lastCode string
	for _, c := range e.runner.Calls() {
		if c.Tool == "RunCode" {
			lastCode = c.Code
		}
	}
	if len(lastCode) <= comparisonContentLimit {
		t.Fatalf("final verification content length = %d, want > %d (full artifact, not the truncated judge head)", len(lastCode), comparisonContentLimit)
	}
}

// TestVerifierFirst_CodeAcceptSkipsJudge is the F4-T3 acceptance bar: with
// judge_mode=verifier and a passing real test run, a fresh code artifact is
// accepted by the deterministic verifier and the LLM judge is never called.
func TestVerifierFirst_CodeAcceptSkipsJudge(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.JudgeMode = config.JudgeModeVerifier
	})
	jobID := e.submitCode(t)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed (verifier accepted)", j.State)
	}
	if got, ok := eventField(t, e, jobID, "verification_decision", "judge_called"); !ok || got != false {
		t.Fatalf("judge_called = %v (found=%v), want false", got, ok)
	}
	if got, ok := eventField(t, e, jobID, "comparison", "winner"); !ok || got != "new" {
		t.Fatalf("winner = %v (found=%v), want new", got, ok)
	}
}

// TestVerifierFirst_FailingTestsBlockAcceptance proves a failed real test run
// is never accepted: with all candidates failing evaluation, the job does not
// complete (it exhausts reflection and fails).
func TestVerifierFirst_FailingTestsBlockAcceptance(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.JudgeMode = config.JudgeModeVerifier
	})
	e.runner.WithExit(1)
	jobID := e.submitCode(t)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State == job.StateCompleted {
		t.Fatalf("state = completed, want a failure (failing tests must never be accepted)")
	}
}

// TestVerifierFirst_SameFamilyPreviousArtifactAccepted proves a
// single-model-family install can still accept later code artifacts. Without
// this, every task after the first has a previous accepted artifact, no
// cross-family judge is available, and the verifier-first path would decline
// an artifact whose real tests pass.
func TestVerifierFirst_SameFamilyPreviousArtifactAccepted(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.JudgeMode = config.JudgeModeVerifier
		// Make the judge share the generator's family. The default test
		// config has three personas; this pins the single-family case.
		cfg.Personas.Security.Model = cfg.Personas.Main.Model
		cfg.Personas.Security.Family = cfg.Personas.Main.Family
	})
	ctx := context.Background()
	projectID, _, firstJob := e.createProjectTask(t, "code",
		"Write a Python module that manages a personal book collection.")
	e.eng.Run(ctx, firstJob)

	first, err := e.jobs.Get(ctx, firstJob)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != job.StateCompleted {
		t.Fatalf("first job state = %s, want completed", first.State)
	}
	firstAccepted, err := e.artifacts.LatestAcceptedByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("first accepted artifact: %v", err)
	}

	// Second task under the same project; it now has a previous accepted
	// artifact and the judge shares the generator's family.
	task, err := e.projects.SubmitGoal(ctx, projectID,
		"Extend the book collection module with a search command.", []string{"tests pass"})
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}
	secondJob, err := e.jobs.Create(ctx, task.ID, projectID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e.eng.Run(ctx, secondJob.ID)

	second, err := e.jobs.Get(ctx, secondJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != job.StateCompleted {
		t.Fatalf("second job state = %s, want completed", second.State)
	}
	secondAccepted, err := e.artifacts.LatestAcceptedByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("second accepted artifact: %v", err)
	}
	if secondAccepted.ID == firstAccepted.ID {
		t.Fatalf("second task did not produce a new accepted artifact (still %s)", firstAccepted.ID)
	}
}

// TestVerifierFirst_SameFamilyJudgeDeclines proves the F4-T3 cross-family
// rule for an artifact with no decisive deterministic verifier: the judge is
// not consulted, and without decisive evidence the new artifact is declined
// with an audited mismatch.
func TestVerifierFirst_SameFamilyJudgeDeclines(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.JudgeMode = config.JudgeModeVerifier
		// Divergence uses `main`; forcing the judge to `main` makes the
		// families identical (mistral-nemo).
		cfg.Execution.JudgePersona = "main"
	})
	jobID := e.submit(t) // text, no criteria → no structural verifier
	e.eng.Run(context.Background(), jobID)

	if _, ok := eventField(t, e, jobID, "judge_family_mismatch", "judge_family"); !ok {
		t.Fatal("no judge_family_mismatch audit row")
	}
	if got, ok := eventField(t, e, jobID, "verification_decision", "judge_called"); !ok || got != false {
		t.Fatalf("judge_called = %v (found=%v), want false", got, ok)
	}
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateFailed {
		t.Fatalf("state = %s, want failed (declined, no previous)", j.State)
	}
}
