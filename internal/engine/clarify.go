package engine

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/notebook"
)

// questionMarker is the single line a model uses to ask the operator for a
// missing fact (ADR-0112). It is deliberately distinctive so planning prose
// containing the word "question" does not park a job.
const questionMarker = "QUESTION:"

// parseQuestion returns the marker's text, or "" when the response has none.
// Only the first marker counts; the question is one line by contract.
func parseQuestion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, questionMarker) {
			return strings.TrimSpace(strings.TrimPrefix(line, questionMarker))
		}
	}
	return ""
}

// askForClarification parks the job when the response carries a QUESTION
// marker and a clarifier is wired. It returns true when the job was parked, so
// the caller returns ErrPaused instead of transitioning the phase.
func (e *Engine) askForClarification(ctx context.Context, jobID, projectID, phase, content string) bool {
	question := parseQuestion(content)
	if question == "" || e.clarifier == nil {
		return false
	}
	e.note(ctx, jobID, projectID, notebook.KindQuestion, phase, question)
	if err := e.clarifier.Ask(ctx, jobID, projectID, phase, question); err != nil {
		slog.Warn("engine: filing clarification", "job", jobID, "phase", phase, "err", err)
		return false
	}
	e.audit(ctx, jobID, map[string]any{
		"event": "clarification_requested", "phase": phase, "question": question,
	})
	return true
}
