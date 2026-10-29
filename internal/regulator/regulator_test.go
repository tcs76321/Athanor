package regulator_test

import (
	"testing"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/regulator"
)

func units() []analysis.Unit {
	return []analysis.Unit{
		{ID: "u1", Goal: "parse expression grammar", Falsifier: "parse the sample input"},
		{ID: "u2", Goal: "operator precedence", Falsifier: "compare 2*3+4 with 2*(3+4)"},
	}
}

func TestDecideRefinesAnUnrefinedUnit(t *testing.T) {
	got := regulator.Decide(regulator.Input{
		Units: units(), Implicated: "u2", Refined: map[string]int{},
		Iterations: 0, MaxIterations: 2,
	})
	if got.Decision != regulator.Refine || got.UnitID != "u2" {
		t.Fatalf("decision = %+v, want refine u2", got)
	}
}

func TestDecideReframesAfterOneRefine(t *testing.T) {
	got := regulator.Decide(regulator.Input{
		Units: units(), Implicated: "u2", Refined: map[string]int{"u2": 1},
		Iterations: 1, MaxIterations: 2,
	})
	if got.Decision != regulator.Reframe {
		t.Fatalf("decision = %+v, want reframe", got)
	}
}

func TestDecideRepeatedSignatureReframesAfterRefine(t *testing.T) {
	got := regulator.Decide(regulator.Input{
		Units: units(), Implicated: "u1", Refined: map[string]int{"u1": 1},
		Signature: "abc", PreviousSignature: "abc",
		Iterations: 1, MaxIterations: 3,
	})
	if got.Decision != regulator.Reframe {
		t.Fatalf("decision = %+v, want reframe", got)
	}
}

func TestDecideRefinesBeforeReframingOnce(t *testing.T) {
	got := regulator.Decide(regulator.Input{
		Units: units(), Implicated: "u1", Refined: map[string]int{},
		Signature: "abc", PreviousSignature: "abc",
		Iterations: 1, MaxIterations: 3,
	})
	if got.Decision != regulator.Refine || got.UnitID != "u1" {
		t.Fatalf("decision = %+v, want the first targeted refine", got)
	}
}

func TestDecideDefersBeforeQuality(t *testing.T) {
	got := regulator.Decide(regulator.Input{BudgetExhausted: true, Iterations: 9, MaxIterations: 2})
	if got.Decision != regulator.Defer {
		t.Fatalf("decision = %+v, want defer", got)
	}
}

func TestDecideAbandonsAtTheCeiling(t *testing.T) {
	got := regulator.Decide(regulator.Input{Iterations: 2, MaxIterations: 2})
	if got.Decision != regulator.Abandon {
		t.Fatalf("decision = %+v, want abandon", got)
	}
}

func TestDecideProbesAnUninformativeFailure(t *testing.T) {
	got := regulator.Decide(regulator.Input{Iterations: 0, MaxIterations: 2})
	if got.Decision != regulator.Probe {
		t.Fatalf("decision = %+v, want probe", got)
	}
}

func TestDecideProbesAFirstDistinctFrameFailure(t *testing.T) {
	got := regulator.Decide(regulator.Input{
		Signature: "new", PreviousSignature: "old", Iterations: 1, MaxIterations: 3,
	})
	if got.Decision != regulator.Probe {
		t.Fatalf("decision = %+v, want probe before reframing", got)
	}
}

func TestAttribute(t *testing.T) {
	if got := regulator.Attribute(units(), "assertion failed: operator precedence is wrong for 2*3+4"); got != "u2" {
		t.Errorf("attributed = %q, want u2", got)
	}
	if got := regulator.Attribute(units(), "the tests failed"); got != "" {
		t.Errorf("unrelated failure attributed to %q, want frame level", got)
	}
}

func TestSignatureIsOrderInsensitive(t *testing.T) {
	a := regulator.Signature([]string{"assertion failed", "exit code 1"})
	b := regulator.Signature([]string{"exit code 1", "assertion failed"})
	if a != b {
		t.Errorf("signatures differ: %s vs %s", a, b)
	}
	if a == regulator.Signature([]string{"different failure"}) {
		t.Error("different failures produced the same signature")
	}
}
