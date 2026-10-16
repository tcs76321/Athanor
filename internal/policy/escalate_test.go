package policy

import "testing"

func escInputs() Inputs {
	return Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 2, HardCandidatesCeiling: 10}}
}

// TestEscalateNoOpOnFirstAttempt pins that the first attempt is never escalated
// (ADR-0074 §3): a first-pass benchmark must be unaffected.
func TestEscalateNoOpOnFirstAttempt(t *testing.T) {
	in := escInputs()
	base := Default{}.Decide(in)
	for _, attempt := range []int{0, 1} {
		got := Escalate(base, attempt, in)
		if got.Candidates != base.Candidates || got.MaxReflectionLoops != base.MaxReflectionLoops ||
			got.BudgetScale != 0 || got.TemperatureDelta != 0 || got.Directive != "" {
			t.Errorf("attempt %d escalated: %+v", attempt, got)
		}
	}
}

// TestEscalateLadder pins the rung order: budget (2), candidates (3),
// reflection (4), model (5), temperature (6), directive (7).
func TestEscalateLadder(t *testing.T) {
	in := escInputs()
	base := Plan{Candidates: 1, MaxReflectionLoops: 0} // an "easy" base plan

	if a2 := Escalate(base, 2, in); a2.BudgetScale != 1.5 || a2.Candidates != 1 {
		t.Errorf("attempt 2 = scale %v candidates %d, want 1.5/1", a2.BudgetScale, a2.Candidates)
	}
	if a3 := Escalate(base, 3, in); a3.Candidates != 10 || a3.MaxReflectionLoops != 0 {
		t.Errorf("attempt 3 = candidates %d reflection %d, want 10/0", a3.Candidates, a3.MaxReflectionLoops)
	}
	if a4 := Escalate(base, 4, in); a4.MaxReflectionLoops != 2 {
		t.Errorf("attempt 4 reflection = %d, want 2", a4.MaxReflectionLoops)
	}
	a5 := Escalate(base, 5, in)
	if a5.ModelRouting[PhaseDiverging] != RoleAlternative || a5.ModelRouting[PhaseSynthesizing] != RoleAlternative {
		t.Errorf("attempt 5 model routing = %v, want an alternative generator/synthesizer", a5.ModelRouting)
	}
	if a6 := Escalate(base, 6, in); a6.TemperatureDelta != retryTemperatureDelta {
		t.Errorf("attempt 6 temperature delta = %v, want %v", a6.TemperatureDelta, retryTemperatureDelta)
	}
	if a7 := Escalate(base, 7, in); a7.Directive == "" {
		t.Error("attempt 7 directive is empty")
	}
}

// TestEscalateBudgetScaleCapped pins the budget cap so the ladder cannot grow
// wall-time without bound.
func TestEscalateBudgetScaleCapped(t *testing.T) {
	if got := Escalate(Plan{}, 99, escInputs()).BudgetScale; got != 2.5 {
		t.Errorf("budget scale at attempt 99 = %v, want 2.5 (cap)", got)
	}
}

// TestEscalateNeverExceedsCeilings pins the bound: escalation only raises toward
// the configured ceilings, never past them.
func TestEscalateNeverExceedsCeilings(t *testing.T) {
	in := Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 2, HardCandidatesCeiling: 5}}
	got := Escalate(Plan{Candidates: 4, MaxReflectionLoops: 1}, 9, in)
	if got.Candidates != 5 {
		t.Errorf("candidates = %d, want 5 (hard ceiling)", got.Candidates)
	}
	if got.MaxReflectionLoops != 2 {
		t.Errorf("reflection = %d, want 2 (ceiling)", got.MaxReflectionLoops)
	}
}
