package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/regulator"
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

func TestRegulatorDirectiveNamesTheUnit(t *testing.T) {
	units := []analysis.Unit{{ID: "u2", Goal: "operator precedence", Reason: "hidden constraints",
		Falsifier: "compare 2*3+4 with 2*(3+4)"}}
	got := regulatorDirective(regulator.Result{Decision: regulator.Refine, UnitID: "u2"}, units)
	for _, want := range []string{"refine unit u2", "operator precedence", "compare 2*3+4"} {
		if !strings.Contains(got, want) {
			t.Errorf("directive %q missing %q", got, want)
		}
	}
	reframe := regulatorDirective(regulator.Result{Decision: regulator.Reframe}, units)
	if !strings.Contains(reframe, "do not repeat") {
		t.Errorf("reframe directive = %q", reframe)
	}
}
