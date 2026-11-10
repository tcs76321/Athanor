package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/verify"
)

func TestShouldFallbackToCandidate(t *testing.T) {
	hardFail := verify.Result{HardApplied: 1, HardPassed: false, Applied: 1, Passed: false, Score: 0.4}
	hardPass := verify.Result{HardApplied: 1, HardPassed: true, Applied: 1, Passed: true, Score: 1}
	softHigher := verify.Result{Applied: 1, Passed: false, Score: 0.8}
	cases := []struct {
		name       string
		wasPassing bool
		final      verify.Result
		candidate  verify.Result
		want       bool
	}{
		{"passing candidate is always kept", true, hardFail, verify.Result{}, true},
		{"final passed", false, hardPass, hardFail, false},
		{"candidate hard-passed", false, hardFail, hardPass, true},
		{"candidate scored higher", false, hardFail, softHigher, true},
		{"candidate scored equal", false, hardFail, verify.Result{Applied: 1, Score: 0.4}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acceptance.ShouldFallbackToCandidate(tc.wasPassing, tc.final, tc.candidate); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOneShotFirstOnlyOnFirstCycle(t *testing.T) {
	e := newEnv(t)
	on := true
	e.cfg.Execution.Policy.OneShotFirst = &on
	projectID, taskID, jobID := e.createProjectTask(t, "text",
		"Write a short brief for the one-shot election.")
	ctx := context.Background()
	if !e.eng.phaseDeps.OneShotFirst(ctx, jobID) {
		t.Fatal("first cycle was not eligible for one-shot-first")
	}
	a, err := e.artifacts.CreateDraftFor(ctx, projectID, taskID, jobID,
		artifact.KindProposal, []byte("candidate"))
	if err != nil {
		t.Fatal(err)
	}
	rec := evaluation.NewRecord(jobID, a.ID)
	rec.Score = 0.5
	if _, err := e.eng.eval.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if e.eng.phaseDeps.OneShotFirst(ctx, jobID) {
		t.Fatal("a job with an evaluation record was still eligible")
	}
}

func TestBestPassingCandidateFallsBackToBestScore(t *testing.T) {
	e := newEnv(t)
	projectID, taskID, jobID := e.createProjectTask(t, "text",
		"Write a short brief that will be evaluated.")
	ctx := context.Background()
	var artifacts []artifact.Artifact
	for i, content := range []string{"worst", "best", "middle"} {
		a, err := e.artifacts.CreateDraftFor(ctx, projectID, taskID, jobID,
			artifact.KindProposal, []byte(content))
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, a)
		rec := evaluation.NewRecord(jobID, a.ID)
		rec.PassedTests = false
		rec.Score = []float64{0.1, 0.9, 0.4}[i]
		if _, err := e.eng.eval.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	got, passed, err := e.eng.phaseDeps.BestPassingCandidate(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if passed {
		t.Fatal("no candidate passed, but bestPassingCandidate reported a pass")
	}
	if got.ID != artifacts[1].ID {
		t.Fatalf("picked %s, want the highest-scoring candidate %s", got.ID, artifacts[1].ID)
	}
}

// TestOneShotFirstGeneratesOneCandidate pins the cascade shape at the engine
// boundary: a passing first attempt must not pay for the full panel. This is
// the behavior the v3 benchmark's early-stop arm measures (ADR-0118/0133).
func TestOneShotFirstGeneratesOneCandidate(t *testing.T) {
	e := newEnv(t)
	on := true
	e.cfg.Execution.Policy.OneShotFirst = &on
	e.cfg.Execution.DivergenceCandidates = 3
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)
	if got := e.ollama.callsByPhase["DIVERGENCE"]; got != 1 {
		t.Fatalf("divergence calls = %d, want 1 (one-shot-first should stop on a passing candidate); calls=%v", got, e.ollama.callsByPhase)
	}
}

// TestFullGeneratesAllCandidates is the control for the cascade test: with
// one-shot-first off, the same job pays for N candidates.
func TestFullGeneratesAllCandidates(t *testing.T) {
	e := newEnv(t)
	off := false
	e.cfg.Execution.Policy.OneShotFirst = &off
	e.cfg.Execution.DivergenceCandidates = 3
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)
	if got := e.ollama.callsByPhase["DIVERGENCE"]; got != 3 {
		t.Fatalf("divergence calls = %d, want 3 (the full arm must generate N candidates); calls=%v", got, e.ollama.callsByPhase)
	}
}

// TestCascadeEscalatesOnFailure pins the other half of the one-shot-first
// policy: a failed first attempt pays for the full panel, not a silent stop.
func TestCascadeEscalatesOnFailure(t *testing.T) {
	e := newEnv(t)
	on := true
	e.cfg.Execution.Policy.OneShotFirst = &on
	e.cfg.Execution.DivergenceCandidates = 3
	zero := 0
	e.cfg.Execution.MaxReflectionLoops = &zero
	e.ollama.evalVerdicts = []string{
		`{"passed":false,"score":0.1,"confidence":0.1,"summary":"first attempt failed"}`,
	}
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)
	// The exact escalation path (a second divergence cycle or self-refine)
	// is policy-dependent; the invariant is that a failed first attempt does
	// not stop after one evaluation.
	if got := e.ollama.callsByPhase["EVALUATION"]; got < 2 {
		t.Fatalf("evaluation calls = %d, want escalation after the failed first attempt; calls=%v", got, e.ollama.callsByPhase)
	}
}
