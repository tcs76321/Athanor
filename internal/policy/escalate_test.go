package policy

import "testing"

// TestEscalateNoOpOnFirstAttempt pins that the first attempt is never escalated
// (ADR-0074 §3): a first-pass benchmark must be unaffected.
func TestEscalateNoOpOnFirstAttempt(t *testing.T) {
	in := Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 2, HardCandidatesCeiling: 10}}
	base := Default{}.Decide(in)
	for _, attempt := range []int{0, 1} {
		got := Escalate(base, attempt, in)
		if got.Candidates != base.Candidates || got.MaxReflectionLoops != base.MaxReflectionLoops {
			t.Errorf("attempt %d escalated: candidates=%d reflection=%d, want %d/%d",
				attempt, got.Candidates, got.MaxReflectionLoops, base.Candidates, base.MaxReflectionLoops)
		}
	}
}

// TestEscalateRaisesCandidatesThenReflection pins the ladder order: attempt 2
// raises candidates to the hard ceiling; attempt 3 also raises a reflection
// budget the base plan had lowered (e.g. an "easy" adaptive plan).
func TestEscalateRaisesCandidatesThenReflection(t *testing.T) {
	in := Inputs{Limits: Limits{CandidatesCeiling: 3, ReflectionCeiling: 2, HardCandidatesCeiling: 10}}
	base := Plan{Candidates: 1, MaxReflectionLoops: 0}

	a2 := Escalate(base, 2, in)
	if a2.Candidates != 10 {
		t.Errorf("attempt 2 candidates = %d, want 10", a2.Candidates)
	}
	if a2.MaxReflectionLoops != 0 {
		t.Errorf("attempt 2 reflection = %d, want 0 (rung not reached)", a2.MaxReflectionLoops)
	}

	a3 := Escalate(base, 3, in)
	if a3.Candidates != 10 || a3.MaxReflectionLoops != 2 {
		t.Errorf("attempt 3 = candidates %d reflection %d, want 10/2", a3.Candidates, a3.MaxReflectionLoops)
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
