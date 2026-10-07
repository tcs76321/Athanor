package strategy

import (
	"context"
	"testing"
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
	ops := []Operation{
		{Name: OpPlan, Phase: "planning", Persona: "tall", Calls: 1, Tokens: 100},
		{Name: OpDiverge, Phase: "diverging", Persona: "main", Calls: 3, Tokens: 900},
		{Name: OpVerify, Phase: "evaluating", Persona: "security", Calls: 1, Tokens: 50, Grounded: true, Passed: true},
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
	if last.Name != OpVerify || !last.Grounded || !last.Passed || last.Tokens != 50 {
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

func TestValidOperation(t *testing.T) {
	if !ValidOperation(OpVerify) {
		t.Error("OpVerify should be a valid operation")
	}
	if ValidOperation("nonsense") {
		t.Error("nonsense should not be a valid operation")
	}
}
