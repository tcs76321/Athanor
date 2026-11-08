package engine

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/notebook"
)

// notebookHistoryLimit is how many recent entries reach one prompt. The
// notebook store keeps more; the prompt gets the working set.
const notebookHistoryLimit = 20

// note appends one working-memory entry. It is best-effort: losing a note
// must never fail a job that can otherwise proceed, but the warning makes the
// loss visible.
func (e *Engine) note(ctx context.Context, jobID, projectID string, kind notebook.Kind, phase, content string) {
	if e.notebook == nil || strings.TrimSpace(content) == "" {
		return
	}
	if _, err := e.notebook.Append(ctx, notebook.Entry{
		JobID: jobID, ProjectID: projectID, Kind: kind, Phase: phase, Content: content,
	}); err != nil {
		slog.Warn("engine: appending notebook entry", "job", jobID, "kind", kind, "err", err)
	}
}
