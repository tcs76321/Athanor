package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
)

// reflectCounterPrefix is the `system_state` key prefix
// for the typed reflection counter that M3-T4 commit 4.1
// introduces. The full key is
// `reflect:counter:<job-id>`; the value is the iteration
// count as a string (e.g. `"2"`).
//
// The typed counter replaces the M3-T1 hack of co-opting
// `jobs.recovery_flag` with a `"reflect-N"` string. That hack
// collided with the engine's other use of the recovery flag
// (the M3-T1 close-out's `reflect-N` clear in `engine.Run`
// required a `HasPrefix("reflect-")` guard, which is a
// known brittle pattern).
const reflectCounterPrefix = "reflect:counter:"

// getReflectCounter reads the typed counter for `jobID` from
// `system_state`. Returns 0 when the row is absent (the
// initial state for a fresh job). A parse error is logged
// and treated as 0 — a corrupt counter must not fail the
// engine.
func (e *Engine) getReflectCounter(ctx context.Context, jobID string) int {
	var value string
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`,
		reflectCounterPrefix+jobID,
	).Scan(&value)
	if err != nil {
		// sql.ErrNoRows: 0 is the correct initial value.
		// Other errors: log and treat as 0. The
		// reflection counter is a best-effort
		// mechanism; a corrupt DB row must not
		// block the engine.
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("engine: reading reflection counter",
				"job", jobID, "err", err)
		}
		return 0
	}
	n, perr := strconv.Atoi(value)
	if perr != nil || n < 0 {
		slog.Warn("engine: parsing reflection counter",
			"job", jobID, "value", value, "err", perr)
		return 0
	}
	return n
}

// setReflectCounter upserts the typed counter for `jobID`.
// Uses `INSERT ... ON CONFLICT DO UPDATE` so the first
// reflection iteration creates the row and subsequent
// iterations update it in place. The transaction is
// implicit in a single statement; no audit event is
// emitted for the counter update itself (a separate
// `reflection` event is emitted by `phaseReflect` per
// iteration; adding another would be noise).
func (e *Engine) setReflectCounter(ctx context.Context, jobID string, n int) error {
	_, err := e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		reflectCounterPrefix+jobID, strconv.Itoa(n),
	)
	if err != nil {
		return fmt.Errorf("setting reflection counter for %s: %w", jobID, err)
	}
	return nil
}

// refineCounterPrefix is the `system_state` key prefix for the M8-T11
// self-refine counter (`refine:counter:<job-id>`). It mirrors the reflection
// counter: a separate bound so repair attempts and re-divergence retries are
// budgeted independently.
const refineCounterPrefix = "refine:counter:"

// getRefineCounter reads the self-refine counter (0 when absent, like
// getReflectCounter).
func (e *Engine) getRefineCounter(ctx context.Context, jobID string) int {
	var value string
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`,
		refineCounterPrefix+jobID,
	).Scan(&value)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("engine: reading refine counter", "job", jobID, "err", err)
		}
		return 0
	}
	n, perr := strconv.Atoi(value)
	if perr != nil || n < 0 {
		slog.Warn("engine: parsing refine counter", "job", jobID, "value", value, "err", perr)
		return 0
	}
	return n
}

// setRefineCounter upserts the self-refine counter.
func (e *Engine) setRefineCounter(ctx context.Context, jobID string, n int) error {
	_, err := e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		refineCounterPrefix+jobID, strconv.Itoa(n),
	)
	if err != nil {
		return fmt.Errorf("setting refine counter for %s: %w", jobID, err)
	}
	return nil
}

// phaseReflect (§13.1 Phase 4, M3-T1): entered only when the
// evaluating phase found no passing candidates. Asks the LLM
// (main/tall/alternative persona, moderate-to-high temperature) what
// went wrong, persists the proposed improvements, and either loops
// back to diverging (within the budget) or transitions the job to
// `failed` (budget exhausted).
//
// §8.2 branch: `reflecting → diverging` (retry) or
// `reflecting → failed` (budget).
func (e *Engine) phaseReflect(ctx context.Context, j job.Job) error {
	if e.eval == nil {
		return errors.New("engine: evaluation repo is nil (M3-T1; tests must pass it)")
	}
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}

	// M3-T4 commit 4.1: the reflection counter now lives in
	// `system_state` (`reflect:counter:<job-id>`), no longer
	// co-opted from `jobs.recovery_flag`. M3-T4 commit 4.2:
	// the budget is read from config with a 2 default; F4-T1c
	// resolves it through the compute policy.
	plan := e.planFor(ctx, j, p, t)
	max := plan.MaxReflectionLoops
	iter := e.getReflectCounter(ctx, j.ID)
	if iter >= max {
		e.audit(ctx, j.ID, map[string]any{
			"event":     "reflection_budget_exhausted",
			"iter":      iter,
			"max":       max,
			"archetype": p.Archetype,
		})
		_, err = e.jobs.Transition(ctx, j.ID, job.StateFailed)
		return err
	}

	// Aggregate the failure context for the LLM: the most recent
	// EvaluationRecord set tells the reflector what failed.
	records, err := e.eval.ListByJob(ctx, j.ID)
	if err != nil {
		return fmt.Errorf("listing evaluation records: %w", err)
	}
	instructions := buildReflectionInstructions(records)

	role := e.roleFor(plan, llm.PhaseReflecting, llm.RoleMain)
	resp, err := e.call(ctx, j, p, t, llm.PhaseReflecting, role, instructions, nil)
	if err != nil {
		return err
	}

	e.audit(ctx, j.ID, map[string]any{
		"event":      "reflection",
		"iter":       iter + 1,
		"candidates": len(records),
		"proposal":   truncateForEvent(resp.Content, 1024),
	})

	// Bump the typed counter. The check below enforces the
	// budget BEFORE re-entering divergence: if the upcoming
	// iteration would exceed `max`, we transition to
	// `failed` now rather than waste another divergence +
	// evaluation cycle that we already know will not count
	// toward a passing job.
	nextIter := iter + 1
	if serr := e.setReflectCounter(ctx, j.ID, nextIter); serr != nil {
		slog.Warn("engine: bumping reflection counter", "err", serr)
	}
	if nextIter >= max {
		e.audit(ctx, j.ID, map[string]any{
			"event":     "reflection_budget_exhausted",
			"iter":      nextIter,
			"max":       max,
			"archetype": p.Archetype,
		})
		_, err = e.jobs.Transition(ctx, j.ID, job.StateFailed)
		return err
	}

	_, err = e.jobs.Transition(ctx, j.ID, job.StateDiverging)
	return err
}

// currentReflectionIteration was the M3-T1 read side of
// the `"reflect-N"` recovery-flag hack. M3-T4 commit 4.1
// replaced it with `getReflectCounter` (typed counter in
// `system_state`). The function is gone; pre-M3-T4
// recovery_flag values are no longer consulted. The
// `engine.Run` `HasPrefix("reflect-")` guard from M3-T1's
// close-out is also gone — `RecoveryFlag` is now solely
// the "interrupted after a crash" annotation.

// buildReflectionInstructions composes the prompt extra for the
// reflection phase. The reflector sees the most recent
// EvaluationRecord set, the missing criteria, and a one-sentence
// summary of each candidate's failure.
func buildReflectionInstructions(records []evaluation.Record) string {
	if len(records) == 0 {
		return "No candidates landed; divergence produced nothing. Suggest a different approach."
	}
	var b strings.Builder
	b.WriteString("REFLECTION. All candidates failed acceptance. The EvaluationRecords:\n")
	for i, r := range records {
		fmt.Fprintf(&b, "  candidate %d (artifact %s): score=%.2f, passed=%v, missing=%v, security=%v, summary=%q\n",
			i+1, r.ArtifactID, r.Score, r.PassedTests, r.MissingCriteria, r.SecurityIssues, r.Summary)
	}
	b.WriteString("\nPropose ONE specific change (a different approach, a missing context, or a hybrid) that the next divergence pass should try. Output prose, not JSON.")
	return b.String()
}

// truncateForEvent bounds the size of an LLM response embedded in
// an audit event. A multi-KB proposal would inflate the events table
// without bound; 1 KB is enough for a post-mortem reader.
func truncateForEvent(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
