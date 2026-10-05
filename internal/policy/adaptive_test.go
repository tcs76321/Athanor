package policy

import "testing"

func decideAdaptive(f Features) Plan {
	return Adaptive{}.Decide(Inputs{
		Features: f,
		Limits:   Limits{CandidatesCeiling: 3, ReflectionCeiling: 2},
	})
}

func TestAdaptive_EasyHintCutsCompute(t *testing.T) {
	got := decideAdaptive(Features{Archetype: "code", CriteriaCount: 2, Difficulty: "easy"})
	if got.Candidates != 1 || got.MaxReflectionLoops != 0 {
		t.Errorf("easy: got candidates=%d reflection=%d, want 1/0", got.Candidates, got.MaxReflectionLoops)
	}
}

func TestAdaptive_HardHintKeepsCeiling(t *testing.T) {
	got := decideAdaptive(Features{Archetype: "code", CriteriaCount: 2, Difficulty: "hard"})
	if got.Candidates != 3 || got.MaxReflectionLoops != 2 {
		t.Errorf("hard: got candidates=%d reflection=%d, want 3/2", got.Candidates, got.MaxReflectionLoops)
	}
}

func TestAdaptive_HardHintBeatsHistory(t *testing.T) {
	// A high accept rate would otherwise read as easy; the explicit hint wins.
	got := decideAdaptive(Features{Difficulty: "hard", RecentSamples: 20, RecentAcceptRate: 1.0, CriteriaCount: 0})
	if got.Candidates != 3 {
		t.Errorf("hard hint: candidates = %d, want 3", got.Candidates)
	}
}

func TestAdaptive_FamiliarClassIsEasy(t *testing.T) {
	got := decideAdaptive(Features{RecentSamples: 5, RecentAcceptRate: 0.92, CriteriaCount: 3})
	if got.Candidates != 1 {
		t.Errorf("familiar class: candidates = %d, want 1", got.Candidates)
	}
}

func TestAdaptive_SparseHistoryFallsThroughToCriteria(t *testing.T) {
	got := decideAdaptive(Features{RecentSamples: 3, RecentAcceptRate: 1.0, CriteriaCount: 3})
	if got.Candidates != 3 {
		t.Errorf("sparse history + 3 criteria: candidates = %d, want 3", got.Candidates)
	}
}

func TestAdaptive_SingleCriterionIsEasy(t *testing.T) {
	if got := decideAdaptive(Features{CriteriaCount: 1}); got.Candidates != 1 {
		t.Errorf("1 criterion: candidates = %d, want 1", got.Candidates)
	}
	if got := decideAdaptive(Features{CriteriaCount: 2}); got.Candidates != 3 {
		t.Errorf("2 criteria, no other signal: candidates = %d, want 3", got.Candidates)
	}
}

func TestAdaptive_NeverExceedsCeiling(t *testing.T) {
	got := Adaptive{}.Decide(Inputs{
		Features: Features{CriteriaCount: 9},
		Limits:   Limits{CandidatesCeiling: 7, ReflectionCeiling: 4},
	})
	if got.Candidates != 7 || got.MaxReflectionLoops != 4 {
		t.Errorf("ceiling: got %d/%d, want 7/4 (adaptive may only lower compute)",
			got.Candidates, got.MaxReflectionLoops)
	}
}
