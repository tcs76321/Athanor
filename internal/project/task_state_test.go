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
