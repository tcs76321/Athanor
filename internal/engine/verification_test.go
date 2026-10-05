package engine

import (
	"context"
	"encoding/json"
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

// TestVerifierFirst_SameFamilyJudgeDeclines proves the F4-T3 cross-family
// rule: a judge persona drawn from the generator's family is not consulted,
// and without decisive deterministic evidence the new artifact is declined
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
