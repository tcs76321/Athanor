package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

// TestAuditSurvivesCanceledContext pins the phase-deadline fix: an audit row
// emitted while a job is unwinding a canceled phase must still land. Before
// the fix, a `context deadline exceeded` from the phase canceled the append and
// the post-mortem lost the reason.
func TestAuditSurvivesCanceledContext(t *testing.T) {
	env := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env.eng.audit(ctx, "job-audit", map[string]any{"event": "phase_failed", "reason": "deadline"})

	events, err := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: "job-audit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want the audit row written from a canceled context", len(events))
	}
}
