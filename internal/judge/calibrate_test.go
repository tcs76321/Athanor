package judge

import (
	"math"
	"testing"
)

func TestSpearmanPerfectAndInverse(t *testing.T) {
	perfect := []Sample{{1, 1}, {2, 2}, {3, 3}, {4, 4}}
	if got := Spearman(perfect); got < 0.999 {
		t.Errorf("perfect agreement = %v, want ~1", got)
	}
	inverse := []Sample{{1, 4}, {2, 3}, {3, 2}, {4, 1}}
	if got := Spearman(inverse); got > -0.999 {
		t.Errorf("inverse = %v, want ~-1", got)
	}
}

func TestSpearmanTiesAndConstant(t *testing.T) {
	// A constant judge (all 1.0) has undefined correlation → 0.
	constant := []Sample{{1, 1}, {2, 1}, {3, 1}}
	if got := Spearman(constant); got != 0 {
		t.Errorf("constant judge = %v, want 0", got)
	}
	// One sample is undefined.
	if got := Spearman([]Sample{{1, 1}}); got != 0 {
		t.Errorf("single sample = %v, want 0", got)
	}
}

func TestMeetsAgreement(t *testing.T) {
	samples := []Sample{{1, 1}, {2, 2}, {3, 3}}
	if !MeetsAgreement(samples, 0.6) {
		t.Error("perfect judge should clear 0.6")
	}
	if MeetsAgreement(samples, 1.5) {
		t.Error("floor above 1 can never be met")
	}
	if !MeetsAgreement([]Sample{{1, 5}}, 0) {
		t.Error("floor <= 0 is the disabled sentinel")
	}
}

func TestKendallTau(t *testing.T) {
	xs := []float64{1, 2, 3, 4}
	if got := KendallTau(xs, []float64{1, 2, 3, 4}); got < 0.999 {
		t.Errorf("perfect tau = %v, want ~1", got)
	}
	if got := KendallTau(xs, []float64{4, 3, 2, 1}); got > -0.999 {
		t.Errorf("inverse tau = %v, want ~-1", got)
	}
	if got := KendallTau(xs, []float64{5, 5, 5, 5}); got != 0 {
		t.Errorf("constant tau = %v, want 0", got)
	}
}

func TestPairAgreement(t *testing.T) {
	votes := []PairVote{
		{AnchorABetter: true, JudgeChoseA: true},
		{AnchorABetter: true, JudgeChoseA: false},
		{AnchorABetter: false, JudgeChoseA: false},
	}
	if got := PairAgreement(votes); math.Abs(got-2.0/3.0) > 1e-9 {
		t.Errorf("PairAgreement = %v, want 2/3", got)
	}
	if got := PairAgreement(nil); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
}
