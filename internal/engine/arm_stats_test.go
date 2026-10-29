package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

func TestArmStats(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	_, _, jobID := env.createProjectTask(t, "text", "arm stats job with enough goal text")

	appendEv := func(data map[string]any) {
		t.Helper()
		if _, err := env.db.AppendEvent(ctx, store.Event{Category: "jobs", JobID: jobID, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	appendEv(map[string]any{"event": "llm_call", "think": false, "reasoning_tokens": 10})
	appendEv(map[string]any{"event": "llm_call", "think": "medium", "reasoning_tokens": 40})
	appendEv(map[string]any{"event": "llm_call", "think": "medium", "reasoning_tokens": 50})
	appendEv(map[string]any{"event": "task_analysis", "deliberate": 2})
	appendEv(map[string]any{"event": "regulator_decision", "decision": "refine"})
	appendEv(map[string]any{"event": "regulator_decision", "decision": "reframe"})

	got := env.eng.armStats(ctx, jobID)
	if got.arm != "medium" {
		t.Errorf("arm = %q, want the dominant medium", got.arm)
	}
	if got.reasoning != 100 {
		t.Errorf("reasoning = %d, want 100", got.reasoning)
	}
	if got.deliberate != 2 {
		t.Errorf("deliberate = %d, want 2", got.deliberate)
	}
	if got.refines != 1 || got.reframes != 1 {
		t.Errorf("regulator moves = %d refine / %d reframe, want 1/1", got.refines, got.reframes)
	}
}

func TestThinkArmNames(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`false`, "off"},
		{`true`, "on"},
		{`"low"`, "low"},
		{`null`, "unset"},
	} {
		if got := thinkArm([]byte(tc.raw)); got != tc.want {
			t.Errorf("thinkArm(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
