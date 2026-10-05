package project

import (
	"context"
	"fmt"
)

// Task lifecycle statuses (§7.3; migration 0017). Only the scheduler writes
// these; the M1-era in_progress/done/cancelled values are gone.
const (
	TaskPending   = "pending"
	TaskReady     = "ready"
	TaskRunning   = "running"
	TaskPaused    = "paused"
	TaskBlocked   = "blocked"
	TaskCompleted = "completed"
	TaskFailed    = "failed"
)

// taskTransitions is the legal task-status edge set. Parent (phase) tasks
// are grouping only and are never scheduled, so only leaves follow it.
// Terminal states have empty rows.
var taskTransitions = map[string][]string{
	TaskPending:   {TaskRunning, TaskBlocked},
	TaskReady:     {TaskRunning, TaskBlocked, TaskPending},
	TaskRunning:   {TaskCompleted, TaskFailed},
	TaskPaused:    {TaskRunning},
	TaskBlocked:   {TaskPending},
	TaskCompleted: {},
	TaskFailed:    {},
}

// ValidTaskStatus reports whether s is a §7.3 task status.
func ValidTaskStatus(s string) bool {
	_, ok := taskTransitions[s]
	return ok
}

// TaskTerminal reports whether no further transition may leave s.
func TaskTerminal(s string) bool {
	return s == TaskCompleted || s == TaskFailed
}

// CanTaskTransition reports whether from → to is a legal task edge. A
// self-loop is never legal (the scheduler treats "already there" as a
// no-op before calling).
func CanTaskTransition(from, to string) bool {
	if from == to {
		return false
	}
	for _, c := range taskTransitions[from] {
		if c == to {
			return true
		}
	}
	return false
}

// ErrIllegalTaskTransition names both statuses.
type ErrIllegalTaskTransition struct {
	From, To string
}

func (e *ErrIllegalTaskTransition) Error() string {
	return fmt.Sprintf("illegal task transition %s → %s", e.From, e.To)
}

// SetTaskStatus moves a task to `to`, enforcing the transition table. A
// task already at `to` is a no-op returning nil, so scheduling and recovery
// replays are idempotent.
func (r *Repo) SetTaskStatus(ctx context.Context, id, to string) error {
	t, err := r.Task(ctx, id)
	if err != nil {
		return err
	}
	if t.Status == to {
		return nil
	}
	if !CanTaskTransition(t.Status, to) {
		return &ErrIllegalTaskTransition{From: t.Status, To: to}
	}
	if _, err := r.store.DB().ExecContext(ctx, `UPDATE tasks SET status = ? WHERE id = ?`, to, id); err != nil {
		return fmt.Errorf("setting task %s status: %w", id, err)
	}
	return nil
}
