package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/regulator"
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

// reflectProposalPrefix is the `system_state` key prefix for the last
// reflection proposal (`reflect:proposal:<job-id>`). The proposal is the
// reflector's prose recommendation for the next divergence pass; phaseDivergeN
// reads it back and injects it so a retry is a genuine second attempt rather
// than a re-roll of the identical prompt.
const reflectProposalPrefix = "reflect:proposal:"

// getReflectionProposal reads the last reflection proposal for `jobID` ("" when
// absent — the first divergence pass has no prior reflection). A read error is
// logged and treated as empty, matching getReflectCounter.
func (e *Engine) getReflectionProposal(ctx context.Context, jobID string) string {
	var value string
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`,
		reflectProposalPrefix+jobID,
	).Scan(&value)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("engine: reading reflection proposal", "job", jobID, "err", err)
		}
		return ""
	}
	return value
}

// setReflectionProposal upserts the last reflection proposal for `jobID`,
// mirroring setReflectCounter (INSERT ... ON CONFLICT DO UPDATE).
func (e *Engine) setReflectionProposal(ctx context.Context, jobID, proposal string) error {
	_, err := e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		reflectProposalPrefix+jobID, proposal,
	)
	if err != nil {
		return fmt.Errorf("setting reflection proposal for %s: %w", jobID, err)
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

	// Aggregate the failure context for the LLM and the regulator: the most
	// recent EvaluationRecord set tells the reflector what failed.
	records, err := e.eval.ListByJob(ctx, j.ID)
	if err != nil {
		return fmt.Errorf("listing evaluation records: %w", err)
	}
	reasons, failureText := failureSignals(records)
	sig := regulator.Signature(reasons)
	analysisState, _ := e.getAnalysis(ctx, j.ID)
	implicated := regulator.Attribute(analysisState.Units, failureText)
	refined := e.getRefinedCounts(ctx, j.ID)
	budgetExhausted := false
	if rem, ok, rerr := e.horizon.Remaining(ctx, j.ID); rerr == nil && ok && rem <= 0 {
		budgetExhausted = true
	}
	decision := regulator.Decide(regulator.Input{
		Units:             analysisState.Units,
		Implicated:        implicated,
		Signature:         sig,
		PreviousSignature: e.getRegulatorSignature(ctx, j.ID),
		Refined:           refined,
		Iterations:        iter,
		MaxIterations:     max,
		BudgetExhausted:   budgetExhausted,
	})
	e.audit(ctx, j.ID, map[string]any{
		"event": "regulator_decision", "decision": string(decision.Decision),
		"unit": decision.UnitID, "reason": decision.Reason,
		"signature": sig, "implicated": implicated, "iter": iter,
	})

	switch decision.Decision {
	case regulator.Defer:
		// Running out of night is not a failed job: keep the state and let a
		// resume (or the next window) continue it.
		if _, terr := e.jobs.Transition(ctx, j.ID, job.StatePaused); terr != nil {
			return terr
		}
		return nil
	case regulator.Abandon:
		_, terr := e.jobs.Transition(ctx, j.ID, job.StateFailed)
		return terr
	}

	instructions := buildReflectionInstructions(records)
	if d := regulatorDirective(decision, analysisState.Units); d != "" {
		instructions = d + "\n\n" + instructions
	}

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

	// H1: persist the proposal so phaseDivergeN can inject it into the next
	// divergence pass. The regulator directive leads it, so a refine or
	// reframe reaches the retry deterministically rather than only through
	// the model's prose.
	proposal := resp.Content
	if d := regulatorDirective(decision, analysisState.Units); d != "" {
		proposal = d + "\n\n" + proposal
	}
	if serr := e.setReflectionProposal(ctx, j.ID, proposal); serr != nil {
		slog.Warn("engine: persisting reflection proposal", "err", serr)
	}

	if decision.Decision == regulator.Refine && decision.UnitID != "" {
		refined[decision.UnitID]++
		if err := e.setRefinedCounts(ctx, j.ID, refined); err != nil {
			slog.Warn("engine: persisting refine counts", "err", err)
		}
	}
	if err := e.setRegulatorSignature(ctx, j.ID, sig); err != nil {
		slog.Warn("engine: persisting failure signature", "err", err)
	}

	// H2: bump the counter, then always re-enter divergence. The budget is
	// enforced on the next reflection entry (`iter >= max` fails there), so
	// `max=N` yields N full reflect → diverge → evaluate retries. Previously
	// the check here failed as soon as `nextIter` reached `max`, so the Nth
	// reflection was computed and then discarded without ever driving a
	// retry.
	nextIter := iter + 1
	if serr := e.setReflectCounter(ctx, j.ID, nextIter); serr != nil {
		slog.Warn("engine: bumping reflection counter", "err", serr)
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
		if len(r.FailedTests) > 0 {
			fmt.Fprintf(&b, "    failure output: %s\n", truncateForEvent(strings.Join(r.FailedTests, "\n"), 600))
		}
	}
	b.WriteString("\nPropose ONE specific change (a different approach, a missing context, or a hybrid) that the next divergence pass should try. Output prose, not JSON.")
	return b.String()
}

// failureSignals flattens an evaluation set into (reasons for a stable
// signature, text for unit attribution).
func failureSignals(records []evaluation.Record) ([]string, string) {
	var reasons []string
	var text strings.Builder
	for _, r := range records {
		if r.Summary != "" {
			reasons = append(reasons, r.Summary)
			text.WriteString(r.Summary)
			text.WriteByte(' ')
		}
		for _, t := range r.FailedTests {
			reasons = append(reasons, t)
			text.WriteString(t)
			text.WriteByte(' ')
		}
		for _, c := range r.MissingCriteria {
			reasons = append(reasons, c)
			text.WriteString(c)
			text.WriteByte(' ')
		}
		for _, s := range r.SecurityIssues {
			reasons = append(reasons, s)
			text.WriteString(s)
			text.WriteByte(' ')
		}
	}
	return reasons, text.String()
}

// regulatorDirective is the deterministic instruction the checkpoint adds to
// reflection and to the next divergence seed.
func regulatorDirective(r regulator.Result, units []analysis.Unit) string {
	switch r.Decision {
	case regulator.Refine:
		for _, u := range units {
			if u.ID != r.UnitID {
				continue
			}
			return fmt.Sprintf(
				"REGULATOR: refine unit %s (%s). Goal: %s. Falsifier: %s. "+
					"Focus the next attempt on this unit and keep the rest working.",
				u.ID, u.Reason, u.Goal, u.Falsifier)
		}
		return "REGULATOR: refine the failing part identified by the evaluation; keep the rest working."
	case regulator.Reframe:
		return "REGULATOR: reframe. The same failure repeated; do not repeat the previous approach. " +
			"Change the strategy or the framing, not just the wording."
	case regulator.Probe:
		return "REGULATOR: probe. The failure reason is uninformative; run the cheapest check " +
			"against the implicated part before a full rewrite."
	default:
		return ""
	}
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
