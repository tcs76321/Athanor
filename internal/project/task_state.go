package project

import (
	"context"
	"database/sql"
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
	TaskRunning:   {TaskCompleted, TaskFailed, TaskPending, TaskBlocked},
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

// ErrConcurrentTaskTransition reports that a task's status changed between a
// read and a compare-and-swap update (another writer won the race). It mirrors
// job.ErrConcurrentTransition: the loser reports the conflict rather than
// silently overwriting.
type ErrConcurrentTaskTransition struct {
	ID   string
	From string
}

func (e *ErrConcurrentTaskTransition) Error() string {
	return fmt.Sprintf("task %s left status %q before the update committed", e.ID, e.From)
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
	// Compare-and-swap against the status read at call time: two concurrent
	// writers (a scheduler replay and a recovery reconcile are separate pool
	// acquisitions) must not both pass validation and silently overwrite.
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE tasks SET status = ? WHERE id = ? AND status = ?`, to, id, t.Status)
	if err != nil {
		return fmt.Errorf("setting task %s status: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &ErrConcurrentTaskTransition{ID: id, From: t.Status}
	}
	return nil
}

// SetTaskStatusTx moves a task from → to inside the caller's transaction, so a
// job insert and the task-status write commit together (known-issues H7). It
// is narrower than SetTaskStatus (which reads and compare-and-swaps on this
// package's pooled handle): the caller supplies the expected `from` status and
// the target `to`, and a zero-row compare-and-swap reports
// ErrConcurrentTaskTransition. A non-legal edge is reported before any write.
func (r *Repo) SetTaskStatusTx(ctx context.Context, tx *sql.Tx, id, from, to string) error {
	if !CanTaskTransition(from, to) {
		return &ErrIllegalTaskTransition{From: from, To: to}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE tasks SET status = ? WHERE id = ? AND status = ?`, to, id, from)
	if err != nil {
		return fmt.Errorf("setting task %s status: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &ErrConcurrentTaskTransition{ID: id, From: from}
	}
	return nil
}

// ResetTask is the operator recovery write behind the scheduler's Reset seam
// (known-issues H8): it returns a `blocked` task to `pending` and records the
// retry baseline, so the task's §7.2 attempts restart from zero. The UPDATE
// is a compare-and-swap on the `blocked` status — a task that left `blocked`
// between the caller's read and this write (e.g. a concurrent reconcile)
// reports a concurrency error rather than being clobbered. A task already
// `pending` is a no-op so a repeated reset is idempotent.
func (r *Repo) ResetTask(ctx context.Context, id string, baseline int) error {
	if baseline < 0 {
		baseline = 0
	}
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE tasks SET status = ?, retry_baseline = ? WHERE id = ? AND status = ?`,
		TaskPending, baseline, id, TaskBlocked)
	if err != nil {
		return fmt.Errorf("resetting task %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// The row was not `blocked` at write time. Distinguish "already
		// pending" (idempotent no-op) from "some other status" (refuse).
		t, err := r.Task(ctx, id)
		if err != nil {
			return err
		}
		if t.Status == TaskPending {
			return nil
		}
		return &ErrIllegalTaskTransition{From: t.Status, To: TaskPending}
	}
	return nil
}
