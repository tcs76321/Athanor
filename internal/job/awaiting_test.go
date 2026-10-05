package job

import (
	"context"
	"testing"
)

// TestAwaitingApprovalResumesToOrigin proves the M6-T4 invariant
// (ADR-0035): entering awaiting_approval records awaiting_from, the
// repository refuses a resume to any other state, and a legal resume clears
// the column.
func TestAwaitingApprovalResumesToOrigin(t *testing.T) {
	r, s := openRepo(t)
	projectID, taskID := seedProjectTask(t, s)
	ctx := context.Background()

	j, err := r.Create(ctx, taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	runTo(t, r, j.ID, StateContextBuilding, StatePlanning)

	aw, err := r.Transition(ctx, j.ID, StateAwaitingApproval)
	if err != nil {
		t.Fatalf("planning → awaiting_approval: %v", err)
	}
	if aw.AwaitingFrom != StatePlanning {
		t.Errorf("awaiting_from = %q, want planning", aw.AwaitingFrom)
	}

	if _, err := r.Transition(ctx, j.ID, StateDiverging); err == nil {
		t.Error("awaiting_approval resumed to a state other than awaiting_from, want rejection")
	}

	resumed, err := r.Transition(ctx, j.ID, StatePlanning)
	if err != nil {
		t.Fatalf("awaiting_approval → planning: %v", err)
	}
	if resumed.AwaitingFrom != "" {
		t.Errorf("awaiting_from not cleared on resume: %q", resumed.AwaitingFrom)
	}
}

// A denied awaiting_approval request fails the job, which is always legal.
func TestAwaitingApprovalCanFail(t *testing.T) {
	r, s := openRepo(t)
	projectID, taskID := seedProjectTask(t, s)
	ctx := context.Background()

	j, err := r.Create(ctx, taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	runTo(t, r, j.ID, StateContextBuilding, StatePlanning)
	if _, err := r.Transition(ctx, j.ID, StateAwaitingApproval); err != nil {
		t.Fatal(err)
	}
	failed, err := r.Transition(ctx, j.ID, StateFailed)
	if err != nil {
		t.Fatalf("awaiting_approval → failed: %v", err)
	}
	if failed.State != StateFailed {
		t.Errorf("state = %s, want failed", failed.State)
	}
}
