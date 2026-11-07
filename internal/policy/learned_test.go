package policy

import "testing"

func TestChooseCandidatesNoHistory(t *testing.T) {
	got, reason := ChooseCandidates(nil, 3, 5, 8)
	if got != 3 || reason == "" {
		t.Fatalf("got %d (%q), want fallback 3", got, reason)
	}
}

func TestChooseCandidatesPrefersStrongArm(t *testing.T) {
	arms := []ArmStat{
		{Candidates: 3, Samples: 20, Accepted: 6},
		{Candidates: 5, Samples: 20, Accepted: 16},
	}
	got, reason := ChooseCandidates(arms, 3, 5, 8)
	if got != 5 {
		t.Fatalf("got %d (%q), want the stronger arm 5", got, reason)
	}
}

func TestChooseCandidatesKeepsFallbackWhenUnderSampled(t *testing.T) {
	arms := []ArmStat{
		{Candidates: 3, Samples: 20, Accepted: 15},
		{Candidates: 5, Samples: 3, Accepted: 3},
	}
	got, _ := ChooseCandidates(arms, 3, 5, 8)
	if got != 3 {
		t.Fatalf("got %d, want fallback 3 (arm 5 is under-sampled)", got)
	}
}

func TestChooseCandidatesClampsCeiling(t *testing.T) {
	arms := []ArmStat{{Candidates: 5, Samples: 100, Accepted: 100}}
	got, _ := ChooseCandidates(arms, 2, 3, 8)
	if got != 2 {
		t.Fatalf("got %d, want fallback 2 (ceiling 3 excludes arm 5)", got)
	}
}

func TestChooseCandidatesTieKeepsFallback(t *testing.T) {
	arms := []ArmStat{
		{Candidates: 3, Samples: 20, Accepted: 10},
		{Candidates: 4, Samples: 20, Accepted: 10},
	}
	got, _ := ChooseCandidates(arms, 3, 5, 8)
	if got != 3 {
		t.Fatalf("got %d, want fallback 3 on a tie", got)
	}
}

func TestChooseCandidatesExploresUnderSampledArm(t *testing.T) {
	// A rarely tried arm with a perfect record should remain eligible even
	// against a strong, well-sampled fallback: the repertoire never collapses.
	arms := []ArmStat{
		{Candidates: 3, Samples: 100, Accepted: 90},
		{Candidates: 5, Samples: 8, Accepted: 8},
	}
	got, reason := ChooseCandidates(arms, 3, 5, 8)
	if got != 5 {
		t.Fatalf("got %d (%q), want the under-sampled arm to stay eligible", got, reason)
	}
}
