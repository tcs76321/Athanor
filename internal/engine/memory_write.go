package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/job"
)

// rememberCompletedJob writes a bounded deterministic memo for a completed
// job (ADR-0114). It is best-effort and idempotent on the memory side; a
// failed write is logged and never changes the job outcome.
func (e *Engine) rememberCompletedJob(ctx context.Context, j job.Job) {
	if e.memoryWriter == nil || j.State != job.StateCompleted {
		return
	}
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		slog.Warn("engine: loading contexts for memory write", "job", j.ID, "err", err)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\nTask: %s\nGoal: %s\n", p.Name, t.Title, p.Goal)
	if e.notebook != nil {
		entries, nerr := e.notebook.Recent(ctx, j.ID, notebookHistoryLimit)
		if nerr == nil {
			b.WriteString("Working notes:\n")
			for _, entry := range entries {
				text := strings.Join(strings.Fields(entry.Content), " ")
				if len(text) > 500 {
					text = text[:500] + "…"
				}
				fmt.Fprintf(&b, "- [%s/%s] %s\n", entry.Kind, entry.Phase, text)
			}
		}
	}
	summary := b.String()
	if len(summary) > 4000 {
		summary = summary[:4000] + "…"
	}
	inserted, err := e.memoryWriter.RememberJobSummary(ctx, j.ProjectID, j.ID, summary)
	if err != nil {
		slog.Warn("engine: remembering completed job", "job", j.ID, "err", err)
		e.audit(ctx, j.ID, map[string]any{"event": "memory_remember_failed", "error": err.Error()})
		return
	}
	e.audit(ctx, j.ID, map[string]any{"event": "memory_remembered", "inserted": inserted, "bytes": len(summary)})
}
