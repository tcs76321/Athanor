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

func TestPairwiseDiagnostics(t *testing.T) {
	pairs := []pairwisePair{
		{Votes: []pairwiseVote{
			{Judge: "a", Forward: "full", Reverse: "full", Winner: "full"},
			{Judge: "b", Forward: "full", Reverse: "full", Winner: "full"},
		}},
		{Votes: []pairwiseVote{
			{Judge: "a", Forward: "single", Reverse: "single", Winner: "single"},
			{Judge: "b", Forward: "full", Reverse: "full", Winner: "full"},
		}},
	}
	agreement, self, usable, diags := pairwiseDiagnostics(pairs, []string{"a", "b"})
	if agreement != 0.5 {
		t.Errorf("agreement = %v, want 0.5", agreement)
	}
	if self != 1 {
		t.Errorf("self consistency = %v, want 1", self)
	}
	if usable != 2 {
		t.Errorf("usable pairs = %d, want 2", usable)
	}
	if len(diags) != 2 {
		t.Fatalf("diags = %+v", diags)
	}
}

func TestPairwisePositionBiasIsVisible(t *testing.T) {
	pairs := []pairwisePair{
		{Votes: []pairwiseVote{
			// Judge "a" always picks A: forward=single, reverse=full.
			{Judge: "a", Forward: "single", Reverse: "full", Winner: "inconsistent"},
			{Judge: "b", Forward: "full", Reverse: "full", Winner: "full"},
		}},
	}
	_, self, usable, diags := pairwiseDiagnostics(pairs, []string{"a", "b"})
	if usable != 0 {
		t.Errorf("usable pairs = %d, want 0 (only one consistent judge)", usable)
	}
	if self != 0.5 {
		t.Errorf("self consistency = %v, want 0.5", self)
	}
	for _, d := range diags {
		if d.Judge == "a" && d.OrderInconsistentRate != 1 {
			t.Errorf("judge a order-inconsistent rate = %v, want 1", d.OrderInconsistentRate)
		}
	}
}

func TestPairwiseClaimSupportedRejectsZeroAgreement(t *testing.T) {
	if pairwiseClaimSupported(0, 5, 3, 0.6) {
		t.Error("zero agreement passed the claim floor")
	}
	if pairwiseClaimSupported(0.6, 2, 3, 0.6) {
		t.Error("too few usable pairs passed the claim floor")
	}
	if !pairwiseClaimSupported(0.6, 3, 3, 0.6) {
		t.Error("a valid claim failed the floor")
	}
}
