package policy

import (
	"math"
	"reflect"
	"testing"

	"github.com/tcs76321/athanor/internal/cognitive"
)

func TestDefault_EchoesLimits(t *testing.T) {
	got := Default{}.Decide(Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 2}})
	if got.Candidates != 3 {
		t.Errorf("Candidates = %d, want 3", got.Candidates)
	}
	if got.MaxReflectionLoops != 2 {
		t.Errorf("MaxReflectionLoops = %d, want 2", got.MaxReflectionLoops)
	}
	if got.JudgeMode != JudgeLLM {
		t.Errorf("JudgeMode = %q, want %q", got.JudgeMode, JudgeLLM)
	}
	if got.JudgeCount != 1 {
		t.Errorf("JudgeCount = %d, want 1", got.JudgeCount)
	}
	if got.ModelRouting[PhaseEvaluating] != "security" || got.ModelRouting[PhaseComparing] != "security" {
		t.Errorf("ModelRouting = %v, want security for evaluating/comparing", got.ModelRouting)
	}
}

func TestDefault_ClampsCandidates(t *testing.T) {
	for _, ceiling := range []int{0, -1} {
		got := Default{}.Decide(Inputs{Limits: Limits{CandidatesCeiling: ceiling}})
		if got.Candidates != 1 {
			t.Errorf("ceiling %d: Candidates = %d, want 1 (never below one)", ceiling, got.Candidates)
		}
	}
}

func TestDefault_ReflectionZeroIsHonored(t *testing.T) {
	got := Default{}.Decide(Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 0}})
	if got.MaxReflectionLoops != 0 {
		t.Errorf("MaxReflectionLoops = %d, want 0 (explicit 0 disables reflection)", got.MaxReflectionLoops)
	}
	neg := Default{}.Decide(Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: -4}})
	if neg.MaxReflectionLoops != 0 {
		t.Errorf("negative ceiling: MaxReflectionLoops = %d, want 0", neg.MaxReflectionLoops)
	}
}

func TestDefault_PureAndTotal(t *testing.T) {
	in := Inputs{Features: Features{Archetype: "code", CriteriaCount: 2}, Limits: Limits{CandidatesCeiling: 5, ReflectionCeiling: 3}}
	a := Default{}.Decide(in)
	b := Default{}.Decide(in)
	if a.Candidates != b.Candidates || a.MaxReflectionLoops != b.MaxReflectionLoops ||
		a.JudgeMode != b.JudgeMode || a.JudgeCount != b.JudgeCount {
		t.Fatalf("Decide is not deterministic: %+v vs %+v", a, b)
	}
	for _, phase := range []string{PhaseEvaluating, PhaseComparing} {
		if a.ModelRouting[phase] != b.ModelRouting[phase] {
			t.Fatalf("routing differs between calls for %s", phase)
		}
	}
}

func TestDefaultReflectionLoops_IsTwo(t *testing.T) {
	if DefaultReflectionLoops != 2 {
		t.Errorf("DefaultReflectionLoops = %d, want 2 (the historical engine fallback)", DefaultReflectionLoops)
	}
}

// TestDefault_OperationsBaseline proves the M8-T8 selection seam: Default
// returns the canonical baseline operation set, and drops reflection when its
// budget is zero (reflection cannot run without budget).
func TestDefault_OperationsBaseline(t *testing.T) {
	got := Default{}.Decide(Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 2}})
	want := []string{
		cognitive.OpPlan, cognitive.OpDiverge, cognitive.OpVerify,
		cognitive.OpReflect, cognitive.OpSynthesize, cognitive.OpCompare,
	}
	if !reflect.DeepEqual(got.Operations, want) {
		t.Errorf("Operations = %v, want %v", got.Operations, want)
	}
	noReflect := Default{}.Decide(Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 0}})
	for _, op := range noReflect.Operations {
		if op == cognitive.OpReflect {
			t.Errorf("Operations contains reflect with a zero budget: %v", noReflect.Operations)
		}
	}
}

// TestNovelty pins the M8-T9 pure novelty curve: 1 with no history, falling
// linearly to 0 at NoveltyFullSamples.
func TestNovelty(t *testing.T) {
	cases := []struct {
		samples int
		want    float64
	}{
		{0, 1}, {1, 0.8}, {4, 0.2}, {5, 0}, {9, 0},
	}
	for _, tc := range cases {
		got := Novelty(Features{RecentSamples: tc.samples})
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("Novelty(samples=%d) = %v, want %v", tc.samples, got, tc.want)
		}
	}
}

// TestEnsureExploration pins the M8-T10 anti-collapse guard: an unproven
// runnable operation is restored even when a selection dropped it, while a
// proven dropped operation stays dropped.
func TestEnsureExploration(t *testing.T) {
	runnable := []string{cognitive.OpPlan, cognitive.OpDiverge, cognitive.OpVerify}
	eligible := []string{cognitive.OpDiverge} // a selection dropped plan and verify
	samples := map[string]int{
		cognitive.OpPlan:    0, // unproven → restored
		cognitive.OpDiverge: 10,
		cognitive.OpVerify:  5, // proven → stays dropped
	}
	got := ensureExploration(eligible, runnable, samples)
	want := []string{cognitive.OpPlan, cognitive.OpDiverge}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ensureExploration = %v, want %v", got, want)
	}
}

// TestAdaptive_DropsReflectionOnEasy proves the adaptive policy narrows the
// eligible set when it makes a task easy.
func TestAdaptive_DropsReflectionOnEasy(t *testing.T) {
	got := Adaptive{}.Decide(Inputs{
		Features: Features{Difficulty: "easy"},
		Limits:   Limits{CandidatesCeiling: 3, ReflectionCeiling: 2},
	})
	if got.MaxReflectionLoops != 0 {
		t.Fatalf("MaxReflectionLoops = %d, want 0", got.MaxReflectionLoops)
	}
	for _, op := range got.Operations {
		if op == cognitive.OpReflect {
			t.Errorf("easy task eligible set contains reflect: %v", got.Operations)
		}
	}
}
