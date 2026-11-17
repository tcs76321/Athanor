package trigger

import (
	"context"
	"log/slog"
	"time"
)

// Launcher starts the cycle a due trigger describes. The daemon supplies the
// adapter over firstuse and the scheduler; tests supply a fake.
type Launcher interface {
	Launch(ctx context.Context, tr Record) (cycleID string, err error)
}

// Runner fires due schedule triggers on a fixed tick. One Core serializes its
// passes, so no extra lock is needed; the at-most-once guarantee lives in
// Repo.MarkRan, not in the runner.
type Runner struct {
	repo     *Repo
	launch   Launcher
	interval time.Duration
	now      func() time.Time
	logger   *slog.Logger
}

// NewRunner returns a runner that ticks every interval (30s when zero).
func NewRunner(repo *Repo, launch Launcher) *Runner {
	return &Runner{
		repo: repo, launch: launch, interval: 30 * time.Second,
		now: time.Now, logger: slog.Default(),
	}
}

// Tick fires every due trigger once and returns how many cycles it launched.
// A trigger is claimed before its launcher runs: a failure is logged and the
// next slot is still advanced, so a broken launch cannot hammer the Core every
// tick.
func (r *Runner) Tick(ctx context.Context) (int, error) {
	now := r.now().UTC()
	due, err := r.repo.Due(ctx, now, 10)
	if err != nil {
		return 0, err
	}
	launched := 0
	for _, tr := range due {
		if err := ctx.Err(); err != nil {
			return launched, err
		}
		loc, lerr := time.LoadLocation(tr.Timezone)
		if lerr != nil {
			loc = time.UTC
		}
		next, ok := NextRunIn(tr.Spec, now, loc)
		if !ok || !next.After(now) {
			r.logger.Warn("trigger: schedule has no future slot", "trigger", tr.ID, "spec", tr.Spec)
			if err := r.repo.SetEnabled(ctx, tr.ID, false); err != nil {
				r.logger.Error("trigger: disabling bad schedule", "trigger", tr.ID, "err", err)
			}
			continue
		}
		if err := r.repo.MarkRan(ctx, tr.ID, tr.NextRunAt, now.Format(timeFormat), next); err != nil {
			r.logger.Error("trigger: claiming slot", "trigger", tr.ID, "err", err)
			continue
		}
		id, err := r.launch.Launch(ctx, tr)
		if err != nil {
			r.logger.Error("trigger: launch failed", "trigger", tr.ID, "project", tr.ProjectID,
				"template", tr.TemplateID, "err", err)
			continue
		}
		r.logger.Info("trigger: cycle launched", "trigger", tr.ID, "cycle", id,
			"project", tr.ProjectID, "template", tr.TemplateID, "slot", tr.NextRunAt)
		launched++
	}
	return launched, nil
}

// Run ticks until ctx is cancelled. The first tick is immediate so a trigger
// that came due while the Core was down fires on boot.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			r.logger.Error("trigger: tick", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
