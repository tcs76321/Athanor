package engine

import (
	"context"
	"testing"
)

func TestRegulatorStateRoundTrip(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	_, _, jobID := env.createProjectTask(t, "text", "regulator state round trip with enough goal text")

	if err := env.eng.state.SetRegulatorSignature(ctx, jobID, "abc123"); err != nil {
		t.Fatal(err)
	}
	if got := env.eng.state.RegulatorSignature(ctx, jobID); got != "abc123" {
		t.Errorf("signature = %q, want abc123", got)
	}
	if err := env.eng.state.SetRefinedCounts(ctx, jobID, map[string]int{"u2": 1}); err != nil {
		t.Fatal(err)
	}
	if got := env.eng.state.RefinedCounts(ctx, jobID); got["u2"] != 1 {
		t.Errorf("refined = %+v, want u2:1", got)
	}

	env.eng.state.Clear(ctx, jobID)
	if got := env.eng.state.RegulatorSignature(ctx, jobID); got != "" {
		t.Errorf("signature after clear = %q, want empty", got)
	}
	if got := env.eng.state.RefinedCounts(ctx, jobID); len(got) != 0 {
		t.Errorf("refined after clear = %+v, want empty", got)
	}
}
