package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/budget"
)

// admitHorizon reserves a horizon-budget grant for a job (ADR-0091). It is
// idempotent while the job holds a live reservation, so both the queue loop and
// Run may call it.
//
// The accounting seam fails open: a ledger error is logged and the job runs.
// Accounting must never be the reason work stops; an exhausted bucket is
// different and returns granted=false so the caller defers.
func (e *Engine) admitHorizon(ctx context.Context, jobID string) (granted bool, retryAfter time.Duration) {
	s := e.cfg.BudgetSettings()
	if e.horizon == nil || !s.Enabled() {
		return true, 0
	}
	if !e.withinHourlyCap(ctx) {
		return false, time.Minute
	}
	w, err := budget.WindowFor(time.Now(), s)
	if err != nil {
		slog.Warn("engine: budget window", "err", err)
		return true, 0
	}
	wall := s.Wall
	if wall > time.Hour {
		wall = time.Hour
	}
	g, ok, err := e.horizon.Reserve(ctx, w, s, jobID, budget.Interactive, s.MaxGrant, wall)
	if err != nil {
		slog.Error("engine: reserving budget", "job", jobID, "err", err)
		return true, 0
	}
	if !ok {
		e.audit(ctx, jobID, map[string]any{
			"event": "job_deferred", "reason": "horizon_budget",
			"window_start": w.Start.UTC().Format(time.RFC3339),
			"window_end":   w.End.UTC().Format(time.RFC3339),
		})
		wait := time.Until(w.End)
		if wait < time.Minute {
			wait = time.Minute
		}
		return false, wait
	}
	e.audit(ctx, jobID, map[string]any{
		"event": "budget_grant", "tokens": g.Tokens,
		"wall_sec": int64(g.Wall / time.Second),
		"window":   w.Start.UTC().Format(time.RFC3339),
	})
	return true, 0
}

// withinHourlyCap enforces limits.max_tasks_per_hour at the same admission
// gate as the budget (ADR-0091 item 9). A query error fails open.
func (e *Engine) withinHourlyCap(ctx context.Context) bool {
	max := e.cfg.Limits.MaxTasksPerHour
	if max <= 0 {
		return true
	}
	var n int
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs
		  WHERE started_at >= strftime('%Y-%m-%dT%H:%M:%fZ','now','-1 hour')`).Scan(&n)
	if err != nil {
		slog.Warn("engine: hourly cap query", "err", err)
		return true
	}
	return n < max
}

// settleHorizon closes the job's grant with its measured token spend. It is
// best-effort and runs on every terminal path (completed, failed, cancelled).
func (e *Engine) settleHorizon(ctx context.Context, jobID string) {
	if e.horizon == nil {
		return
	}
	tokens, _ := e.eventStats(ctx, jobID)
	if err := e.horizon.Settle(ctx, jobID, int64(tokens), 0); err != nil {
		slog.Error("engine: settling budget", "job", jobID, "err", err)
	}
}
