package phases

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/regulator"
)

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
