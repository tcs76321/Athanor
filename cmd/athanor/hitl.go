package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/project"
)

// hitlEscalator implements the M6-T3 scheduler.Escalator seam: an exhausted
// task becomes a high-severity `task_escalation` request (M6-T4, ADR-0035).
type hitlEscalator struct{ repo *hitl.Repo }

func (e hitlEscalator) Escalate(ctx context.Context, t project.Task, reason string) error {
	raw, err := json.Marshal(map[string]any{
		"reason": reason, "task_id": t.ID, "goal_id": t.GoalID, "title": t.Title,
	})
	if err != nil {
		return err
	}
	_, err = e.repo.Create(ctx, hitl.Request{
		ProjectID: t.ProjectID, Type: hitl.TypeTaskEscalation, Severity: hitl.SeverityHigh,
		PayloadJSON: string(raw),
	})
	return err
}

// startHITLExpiry runs the M6-T4 expiry sweep until ctx is cancelled. An
// expiring request denies by default, so a HITL wait can never succeed
// merely by being ignored.
func startHITLExpiry(ctx context.Context, svc *hitl.Service, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := svc.Expire(ctx)
				if err != nil {
					log.Error("hitl: expiry sweep", "err", err)
				} else if n > 0 {
					log.Info("hitl: expired requests denied", "count", n)
				}
			}
		}
	}()
}
