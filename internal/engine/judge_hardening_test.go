package engine

import (
	"testing"

	"github.com/tcs76321/athanor/internal/verify"
)

// TestResolveRewardHack pins the F4-T4 reward-hacking guard: a decisive
// verifier failure overrides an LLM "new", and nothing else is touched.
func TestResolveRewardHack(t *testing.T) {
	failed := verify.Result{Applied: 1, Passed: false, Verifiers: []string{"tests"}}
	passed := verify.Result{Applied: 1, Passed: true, Verifiers: []string{"tests"}}
	none := verify.Result{Applied: 0}

	got, overridden := resolveRewardHack(comparisonVerdict{Winner: "new"}, failed, true)
	if !overridden || got.Winner != "previous" {
		t.Errorf("failed verifier + previous: got %+v overridden=%v, want previous/true", got, overridden)
	}
	got, overridden = resolveRewardHack(comparisonVerdict{Winner: "new"}, failed, false)
	if !overridden || got.Winner != "none" {
		t.Errorf("failed verifier + no previous: got %+v overridden=%v, want none/true", got, overridden)
	}
	if _, overridden := resolveRewardHack(comparisonVerdict{Winner: "new"}, passed, false); overridden {
		t.Error("passing verifier must not override")
	}
	if _, overridden := resolveRewardHack(comparisonVerdict{Winner: "new"}, none, false); overridden {
		t.Error("non-decisive verifier must not override")
	}
	if _, overridden := resolveRewardHack(comparisonVerdict{Winner: "previous"}, failed, true); overridden {
		t.Error("a non-new verdict must not be overridden")
	}
}

// TestMajorityWinner pins the F4-T4 quorum aggregation (2-of-3).
func TestMajorityWinner(t *testing.T) {
	cases := []struct {
		votes []string
		want  string
	}{
		{[]string{"new", "new", "previous"}, "new"},
		{[]string{"new", "previous", "none"}, ""},
		{[]string{"previous", "previous", "new"}, "previous"},
		{[]string{"new"}, "new"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := majorityWinner(c.votes); got != c.want {
			t.Errorf("majorityWinner(%v) = %q, want %q", c.votes, got, c.want)
		}
	}
}
