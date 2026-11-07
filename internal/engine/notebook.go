package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/notebook"
)

// notebookHistoryLimit is how many recent entries reach one prompt. The
// notebook store keeps more; the prompt gets the working set.
const notebookHistoryLimit = 20

// notebookPromptBytes bounds the total notebook text injected per call.
const notebookPromptBytes = 4096

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

// notebookLines renders the recent notebook for the prompt in chronological
// order, one bounded line per entry. Newlines are collapsed so one entry
// cannot masquerade as several.
func (e *Engine) notebookLines(ctx context.Context, jobID string) []string {
	if e.notebook == nil {
		return nil
	}
	entries, err := e.notebook.Recent(ctx, jobID, notebookHistoryLimit)
	if err != nil {
		slog.Warn("engine: reading notebook", "job", jobID, "err", err)
		return nil
	}
	var out []string
	total := 0
	for _, entry := range entries {
		text := strings.Join(strings.Fields(entry.Content), " ")
		if text == "" {
			continue
		}
		if len(text) > 800 {
			text = text[:800] + "…"
		}
		phase := entry.Phase
		if phase == "" {
			phase = "job"
		}
		line := fmt.Sprintf("[%s/%s] %s", entry.Kind, phase, text)
		if total+len(line)+1 > notebookPromptBytes {
			break
		}
		out = append(out, line)
		total += len(line) + 1
	}
	return out
}
