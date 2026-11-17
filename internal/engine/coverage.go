package engine

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/coverage"
	"github.com/tcs76321/athanor/internal/job"
)

// recordCoverage is the retain side of the recurring-cycle ledger (ADR-0141):
// a completed job whose task declares a coverage namespace and whose accepted
// artifact carries a ```coverage block upserts those items. Every failure is a
// warning; the cycle has already completed and the next run simply starts with
// an empty recent set.
func (e *Engine) recordCoverage(ctx context.Context, j job.Job) {
	if e.coverage == nil || e.artifacts == nil || j.State != job.StateCompleted {
		return
	}
	_, t, err := e.contexts(ctx, j)
	if err != nil {
		return
	}
	ns := coverageNamespace(t.Description)
	if ns == "" {
		return
	}
	arts, err := e.artifacts.ListByJob(ctx, j.ID)
	if err != nil {
		return
	}
	for _, a := range arts {
		if a.Status != artifact.StatusAccepted {
			continue
		}
		content, rerr := e.artifacts.ReadContent(ctx, a.ID)
		if rerr != nil {
			continue
		}
		items := coverage.ParseItems(string(content))
		if len(items) == 0 {
			continue
		}
		if err := e.coverage.Record(ctx, j.ProjectID, ns, items); err != nil {
			slog.Warn("engine: recording coverage", "job", j.ID, "namespace", ns, "err", err)
			return
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "coverage_recorded", "namespace": ns, "items": len(items),
		})
		return
	}
}

// coverageNamespace reads the compile-time marker a retain block carries.
func coverageNamespace(description string) string {
	const marker = "COVERAGE NAMESPACE:"
	for _, line := range strings.Split(description, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= len(marker) && strings.EqualFold(trimmed[:len(marker)], marker) {
			return strings.TrimSpace(trimmed[len(marker):])
		}
	}
	return ""
}
