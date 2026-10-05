package job

import (
	"context"
	"fmt"
)

// Transition moves a job from its current state to `to`, enforcing:
//
//   - legality per the M1 transition table (ADR-0001),
//   - the paused-resume invariant (paused resumes to paused_from),
//   - atomicity: the state update, started_at/finished_at bookkeeping,
//     and the audit event commit together (§8.2, ADR-0006),
//   - compare-and-swap against the state read at call time, so a
//     concurrent transition loses loudly instead of overwriting.
//
// Race note: when `current.State == to` at read time, the caller
// is observing the same state another writer has just committed
// (the §8.2 state machine has no self-loops, so no production
// path ever requests `state -> state`). We return
// ErrConcurrentTransition rather than IllegalTransitionError
// because the precondition check is the wrong layer to report
// a race through — the loser of two concurrent transitions
// consistently sees the race error, not a self-loop error.
// ValidateTransition still rejects `from == to` (the table
// is unchanged; `state_test.go` pins the contract), and
// `state_test.go`'s self-loop assertions are unaffected
// because they call ValidateTransition directly, not through
// Repository.Transition.
func (r *Repository) Transition(ctx context.Context, id string, to State) (Job, error) {
	current, err := r.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if current.State == to {
		// Same-state at read time: the only way this happens in
		// production is when another writer moved the job to
		// exactly the target state between our read and our
		// transition. The CAS below would also catch it
		// (RowsAffected == 0), but the precondition check
		// would otherwise surface this as
		// IllegalTransitionError — the wrong error type for a
		// race. Report ErrConcurrentTransition so the loser
		// consistently sees the race.
		return Job{}, fmt.Errorf("%w: job %s already in state %q", ErrConcurrentTransition, id, to)
	}

	if err := ValidateTransition(current.State, to); err != nil {
		return Job{}, err
	}
	// Paused jobs resume to exactly the state they were paused from
	// (cancellation stays legal from paused).
	if current.State == StatePaused && to != StateCancelled && to != current.PausedFrom {
		return Job{}, fmt.Errorf("paused job %s must resume to %q (its paused_from), not %q",
			id, current.PausedFrom, to)
	}
	// awaiting_approval jobs resume to exactly the state they waited from;
	// a denied (rejected or expired) request fails the job. The HITL
	// service is the only writer that resumes, and the repository enforces
	// the invariant so a hand-edited row cannot resume elsewhere.
	if current.State == StateAwaitingApproval && to != StateCancelled && to != StateFailed && to != current.AwaitingFrom {
		return Job{}, fmt.Errorf("awaiting-approval job %s must resume to %q (its awaiting_from), not %q",
			id, current.AwaitingFrom, to)
	}

	var pausedFrom, awaitingFrom any // SQL NULL otherwise
	if to == StatePaused {
		pausedFrom = string(current.State)
	}
	if to == StateAwaitingApproval {
		awaitingFrom = string(current.State)
	}

	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return Job{}, fmt.Errorf("beginning transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE jobs SET
			state = ?,
			paused_from = ?,
			awaiting_from = ?,
			started_at  = CASE WHEN ? = 'context_building' AND started_at IS NULL
			                   THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') ELSE started_at END,
			finished_at = CASE WHEN ? IN ('completed','failed','cancelled')
			                   THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') ELSE finished_at END
		WHERE id = ? AND state = ?`,
		string(to), pausedFrom, awaitingFrom, string(to), string(to), id, string(current.State),
	)
	if err != nil {
		return Job{}, fmt.Errorf("transitioning job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Job{}, fmt.Errorf("counting affected rows: %w", err)
	}
	if n == 0 {
		// The row exists (Get succeeded) but its state changed: another
		// writer won the CAS.
		return Job{}, fmt.Errorf("%w: job %s left state %q before the update committed", ErrConcurrentTransition, id, current.State)
	}

	if err := r.appendEvent(ctx, tx, id, current.ProjectID, map[string]any{
		"event": "transition", "from": string(current.State), "to": string(to),
	}); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, fmt.Errorf("committing transition: %w", err)
	}
	return r.Get(ctx, id)
}

// SetRecoveryFlag records a recovery annotation on the job (§8.3:
// `interrupted` after a crash, cleared on successful resume).
func (r *Repository) SetRecoveryFlag(ctx context.Context, id, flag string) error {
	res, err := r.store.DB().ExecContext(ctx, `UPDATE jobs SET recovery_flag = ? WHERE id = ?`, flag, id)
	if err != nil {
		return fmt.Errorf("setting recovery flag: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}
