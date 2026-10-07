package strategy

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/cognitive"
)

// TestOutcomeOperationsRoundTrip proves the M8-T7 trajectory column: the
// executed operations persist and read back through GetOutcomeByJob and
// ListOutcomes, with cost and grounded verdicts intact.
func TestOutcomeOperationsRoundTrip(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	projectID, jobID := createJob(t, s)

	if _, err := repo.CreateProfile(ctx, Profile{JobID: jobID, ProjectID: projectID, Archetype: "code"}); err != nil {
		t.Fatal(err)
	}
	ops := []cognitive.Operation{
		{Name: cognitive.OpPlan, Phase: "planning", Persona: "tall", Calls: 1, Tokens: 100},
		{Name: cognitive.OpDiverge, Phase: "diverging", Persona: "main", Calls: 3, Tokens: 900},
		{Name: cognitive.OpVerify, Phase: "evaluating", Persona: "security", Calls: 1, Tokens: 50, Grounded: true, Passed: true},
	}
	out, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: ResultAcceptedNew, Operations: ops})
	if err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}
	if len(out.Operations) != 3 {
		t.Fatalf("operations = %+v, want 3", out.Operations)
	}

	got, err := repo.GetOutcomeByJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	last := got.Operations[2]
	if last.Name != cognitive.OpVerify || !last.Grounded || !last.Passed || last.Tokens != 50 {
		t.Errorf("round-trip operations = %+v, want the verify op with grounded=true passed=true", got.Operations)
	}
	if got.Operations[1].Calls != 3 {
		t.Errorf("diverge calls = %d, want 3", got.Operations[1].Calls)
	}

	list, err := repo.ListOutcomes(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Operations) != 3 {
		t.Errorf("ListOutcomes operations = %+v, want one outcome with 3 operations", list)
	}
}

// TestOutcomeOperationsDefaultEmpty proves a legacy/empty trajectory reads
// back as an empty non-nil slice (the migration default).
func TestOutcomeOperationsDefaultEmpty(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	projectID, jobID := createJob(t, s)
	if _, err := repo.CreateProfile(ctx, Profile{JobID: jobID, ProjectID: projectID, Archetype: "code"}); err != nil {
		t.Fatal(err)
	}
	out, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: ResultAcceptedNew})
	if err != nil {
		t.Fatal(err)
	}
	if out.Operations == nil || len(out.Operations) != 0 {
		t.Errorf("operations = %#v, want empty non-nil", out.Operations)
	}
}

// TestOperationSamples proves the M8-T10 input: per-operation counts over
// recent outcomes for an archetype, counted once per outcome.
func TestOperationSamples(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	projectID, jobID := createJob(t, s)
	if _, err := repo.CreateProfile(ctx, Profile{JobID: jobID, ProjectID: projectID, Archetype: "code"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: ResultAcceptedNew, Operations: []cognitive.Operation{
		{Name: cognitive.OpPlan}, {Name: cognitive.OpDiverge}, {Name: cognitive.OpDiverge},
	}}); err != nil {
		t.Fatal(err)
	}
	counts, err := repo.OperationSamples(ctx, "code", 10)
	if err != nil {
		t.Fatal(err)
	}
	if counts[cognitive.OpPlan] != 1 {
		t.Errorf("op plan samples = %d, want 1", counts[cognitive.OpPlan])
	}
	if counts[cognitive.OpDiverge] != 1 {
		t.Errorf("op diverge samples = %d, want 1 (once per outcome)", counts[cognitive.OpDiverge])
	}
	if counts[cognitive.OpVerify] != 0 {
		t.Errorf("op verify samples = %d, want 0 (absent)", counts[cognitive.OpVerify])
	}
}
