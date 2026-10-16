package project

import (
	"context"
	"errors"
	"testing"
)

func TestTaskStatusTransitions(t *testing.T) {
	legal := [][2]string{
		{TaskPending, TaskRunning},
		{TaskPending, TaskBlocked},
		{TaskRunning, TaskCompleted},
		{TaskRunning, TaskFailed},
		{TaskRunning, TaskPending},
		{TaskRunning, TaskBlocked},
		{TaskBlocked, TaskPending},
	}
	for _, e := range legal {
		if !CanTaskTransition(e[0], e[1]) {
			t.Errorf("%s → %s should be legal", e[0], e[1])
		}
	}
	illegal := [][2]string{
		{TaskPending, TaskCompleted},
		{TaskCompleted, TaskRunning},
		{TaskFailed, TaskPending},
		{TaskRunning, TaskRunning},
		{TaskPending, TaskPending},
		{TaskBlocked, TaskRunning},
	}
	for _, e := range illegal {
		if CanTaskTransition(e[0], e[1]) {
			t.Errorf("%s → %s should be illegal", e[0], e[1])
		}
	}
	if !ValidTaskStatus(TaskCompleted) || !ValidTaskStatus(TaskFailed) || ValidTaskStatus("done") {
		t.Error("ValidTaskStatus disagrees with the §7.3 set")
	}
	if !TaskTerminal(TaskCompleted) || !TaskTerminal(TaskFailed) || TaskTerminal(TaskPending) {
		t.Error("TaskTerminal disagrees with the terminal set")
	}
}

func TestSetTaskStatus(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()
	p, _, err := r.Create(ctx, "demo", ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, tasks, err := r.CreateDAG(ctx, p.ID, "Build a small REST endpoint with tests.", nil,
		[]TaskSpec{{Key: "a", Title: "a", Criteria: []string{"c"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := tasks[0].ID

	if err := r.SetTaskStatus(ctx, id, TaskRunning); err != nil {
		t.Fatalf("pending→running: %v", err)
	}
	if err := r.SetTaskStatus(ctx, id, TaskRunning); err != nil {
		t.Fatalf("idempotent set: %v", err)
	}
	if err := r.SetTaskStatus(ctx, id, TaskCompleted); err != nil {
		t.Fatalf("running→completed: %v", err)
	}
	var illegal *ErrIllegalTaskTransition
	if err := r.SetTaskStatus(ctx, id, TaskRunning); !errors.As(err, &illegal) {
		t.Fatalf("completed→running err = %v, want ErrIllegalTaskTransition", err)
	}
	if err := r.SetTaskStatus(ctx, "missing", TaskRunning); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task err = %v, want ErrNotFound", err)
	}
}

// TestResetTask covers the known-issues H8 operator write: blocked → pending
// with a recorded retry baseline, an idempotent no-op on an already-pending
// task, and a refusal on a terminal task.
func TestResetTask(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()
	p, _, err := r.Create(ctx, "demo", ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, tasks, err := r.CreateDAG(ctx, p.ID, "Build a small REST endpoint with tests.", nil,
		[]TaskSpec{{Key: "a", Title: "a", Criteria: []string{"c"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := tasks[0].ID

	if err := r.SetTaskStatus(ctx, id, TaskBlocked); err != nil {
		t.Fatal(err)
	}
	if err := r.ResetTask(ctx, id, 3); err != nil {
		t.Fatalf("reset blocked: %v", err)
	}
	got, err := r.Task(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != TaskPending || got.RetryBaseline != 3 {
		t.Fatalf("after reset status=%q baseline=%d, want pending/3", got.Status, got.RetryBaseline)
	}
	// Already pending: a no-op that must not rewrite the baseline.
	if err := r.ResetTask(ctx, id, 5); err != nil {
		t.Fatalf("reset pending: %v", err)
	}
	if got, _ := r.Task(ctx, id); got.RetryBaseline != 3 {
		t.Fatalf("idempotent reset changed baseline to %d, want 3", got.RetryBaseline)
	}
	// Terminal: refuse.
	if err := r.SetTaskStatus(ctx, id, TaskRunning); err != nil {
		t.Fatal(err)
	}
	if err := r.SetTaskStatus(ctx, id, TaskCompleted); err != nil {
		t.Fatal(err)
	}
	var illegal *ErrIllegalTaskTransition
	if err := r.ResetTask(ctx, id, 0); !errors.As(err, &illegal) {
		t.Fatalf("reset completed err = %v, want ErrIllegalTaskTransition", err)
	}
}

// TestSetTaskStatusTx covers the in-transaction status write the scheduler uses
// to make a job insert and the task-going-running write atomic (known-issues
// H7): success, a stale-`from` compare-and-swap conflict, and an illegal edge.
func TestSetTaskStatusTx(t *testing.T) {
	r, s := openRepo(t)
	ctx := context.Background()
	p, _, err := r.Create(ctx, "demo", ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, tasks, err := r.CreateDAG(ctx, p.ID, "Build a small REST endpoint with tests.", nil,
		[]TaskSpec{{Key: "a", Title: "a", Criteria: []string{"c"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := tasks[0].ID

	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetTaskStatusTx(ctx, tx, id, TaskPending, TaskRunning); err != nil {
		t.Fatalf("pending→running in tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// A stale `from` loses the compare-and-swap.
	tx2, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var conc *ErrConcurrentTaskTransition
	if err := r.SetTaskStatusTx(ctx, tx2, id, TaskPending, TaskRunning); !errors.As(err, &conc) {
		t.Fatalf("stale from err = %v, want ErrConcurrentTaskTransition", err)
	}
	_ = tx2.Rollback()

	// An illegal edge is rejected before any write.
	tx3, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var illegal *ErrIllegalTaskTransition
	if err := r.SetTaskStatusTx(ctx, tx3, id, TaskCompleted, TaskPending); !errors.As(err, &illegal) {
		t.Fatalf("illegal edge err = %v, want ErrIllegalTaskTransition", err)
	}
	_ = tx3.Rollback()
}
