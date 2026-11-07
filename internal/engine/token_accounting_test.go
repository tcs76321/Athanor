package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

// TestEventStatsDoesNotDoubleCountReasoning pins the Ollama accounting
// contract: eval_count already includes hidden reasoning, so the separate
// estimate is telemetry only and must not be added to the job total.
func TestEventStatsDoesNotDoubleCountReasoning(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.db.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: "job-1", Data: map[string]any{
			"event": "llm_call", "prompt_tokens": 100,
			"completion_tokens": 250, "reasoning_tokens": 80,
		},
	}); err != nil {
		t.Fatal(err)
	}
	tokens, _ := e.eng.eventStats(ctx, "job-1")
	if tokens != 350 {
		t.Fatalf("tokens = %d, want 350 (prompt + completion, reasoning already included)", tokens)
	}
}
