package policy

import "testing"

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
