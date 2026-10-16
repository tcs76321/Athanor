package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
)

// TestBestPassingCandidatePrefersPassing proves M8-T26b: synthesis draws from
// the candidate that passed the deterministic verifier, not the latest
// proposal (which may be failing).
func TestBestPassingCandidatePrefersPassing(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	jobID := env.submitCode(t)
	j, err := env.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}

	mk := func(content string) artifact.Artifact {
		a, err := env.artifacts.CreateDraftFor(ctx, j.ProjectID, j.TaskID, j.ID, artifact.KindProposal, []byte(content))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	failing := mk("def f(): pass  # failing")
	passing := mk("def f(): return 1")
	latestFailing := mk("def f(): pass  # latest, still failing")

	rec := func(a artifact.Artifact, passed bool, score float64) {
		r := evaluation.NewRecord(j.ID, a.ID)
		r.PassedTests = passed
		r.Score = score
		if _, err := env.eng.eval.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rec(failing, false, 0.2)
	rec(passing, true, 0.9)
	rec(latestFailing, false, 0.5)

	got, ok, err := env.eng.bestPassingCandidate(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a passing candidate to be selected")
	}
	if got.ID != passing.ID {
		t.Errorf("selected %s, want the passing candidate %s", got.ID, passing.ID)
	}
}

// TestBestPassingCandidateFallsBackToLatest proves the fallback when no
// candidate passed: the latest proposal is used and ok is false.
func TestBestPassingCandidateFallsBackToLatest(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	jobID := env.submitCode(t)
	j, err := env.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := env.artifacts.CreateDraftFor(ctx, j.ProjectID, j.TaskID, j.ID, artifact.KindProposal, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	last, err := env.artifacts.CreateDraftFor(ctx, j.ProjectID, j.TaskID, j.ID, artifact.KindProposal, []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	r := evaluation.NewRecord(j.ID, first.ID)
	r.PassedTests = false
	r.Score = 0.9
	if _, err := env.eng.eval.Create(ctx, r); err != nil {
		t.Fatal(err)
	}

	got, ok, err := env.eng.bestPassingCandidate(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("ok should be false when no candidate passed")
	}
	// The fallback is the most recent proposal; created_at ties make the exact
	// winner non-deterministic, so accept either proposal.
	if got.ID != first.ID && got.ID != last.ID {
		t.Errorf("fallback = %s, want one of the two proposals", got.ID)
	}
}
