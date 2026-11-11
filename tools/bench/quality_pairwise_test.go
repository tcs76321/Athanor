package main

import "testing"

func TestReconcilePairWinner(t *testing.T) {
	cases := []struct {
		forward, reverse, want string
	}{
		{"full", "full", "full"},
		{"single", "single", "single"},
		{"tie", "tie", "tie"},
		{"tie", "full", "full"},
		{"single", "tie", "single"},
		{"single", "full", "inconsistent"},
	}
	for _, c := range cases {
		if got := reconcilePairWinner(c.forward, c.reverse); got != c.want {
			t.Errorf("reconcilePairWinner(%q, %q) = %q, want %q", c.forward, c.reverse, got, c.want)
		}
	}
}

func TestPairwiseAgreementAndSelfConsistency(t *testing.T) {
	pairs := []pairwisePair{
		{Votes: []pairwiseVote{
			{Forward: "full", Reverse: "full", Winner: "full"},
			{Forward: "full", Reverse: "full", Winner: "full"},
		}},
		{Votes: []pairwiseVote{
			{Forward: "single", Reverse: "single", Winner: "single"},
			{Forward: "full", Reverse: "single", Winner: "single"},
		}},
	}
	agreement, self := pairwiseAgreement(pairs, 2)
	if agreement != 1 {
		t.Errorf("agreement = %v, want 1", agreement)
	}
	if self != 0.75 {
		t.Errorf("self consistency = %v, want 0.75", self)
	}
}
