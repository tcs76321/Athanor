// Package judge holds F4-T4's judge-quality math. The M3-T7 probe found the
// engine's confidence signal degenerate (a near-constant ~0.99) and the
// third-party judges saturated at 1.0, so a judge cannot be trusted just
// because it answers. This package measures how well a judge's scores agree
// with the human-anchor set (eval/anchor) and lets a caller reject a judge
// that does not clear the floor.
//
// It is pure (stdlib only): no model calls, no I/O. The probe and any future
// promotion gate consume it.
package judge

import (
	"math"
	"sort"
)

// Sample pairs one artifact's human-anchor rating with a judge's score for
// the same artifact.
type Sample struct {
	AnchorRating float64
	JudgeScore   float64
}

// Spearman returns Spearman's rank correlation between the anchor ratings
// and the judge scores, using average ranks for ties. It returns 0 when
// there are fewer than two samples or when either series is constant (the
// correlation is undefined). A value near 1 means the judge orders artifacts
// the way the human anchor does; near 0 means no agreement; negative means
// inversion.
func Spearman(samples []Sample) float64 {
	n := len(samples)
	if n < 2 {
		return 0
	}
	anchor := make([]float64, n)
	scores := make([]float64, n)
	for i, s := range samples {
		anchor[i] = s.AnchorRating
		scores[i] = s.JudgeScore
	}
	ra := ranks(anchor)
	rb := ranks(scores)
	ma := mean(ra)
	mb := mean(rb)
	var num, da, db float64
	for i := range ra {
		num += (ra[i] - ma) * (rb[i] - mb)
		da += (ra[i] - ma) * (ra[i] - ma)
		db += (rb[i] - mb) * (rb[i] - mb)
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// MeetsAgreement reports whether the judge clears the agreement floor on the
// anchor set. A floor <= 0 accepts any judge (the disabled sentinel).
func MeetsAgreement(samples []Sample, floor float64) bool {
	if floor <= 0 {
		return true
	}
	return Spearman(samples) >= floor
}

// KendallTau returns Kendall's tau-b between two equal-length series
// (tie-aware). It returns 0 when the lengths differ, n < 2, or either series
// is constant. Like Spearman it measures rank agreement; unlike Spearman it
// is built from pairwise concordance, which is what the pairwise judge
// protocol produces directly.
func KendallTau(xs, ys []float64) float64 {
	n := len(xs)
	if n < 2 || len(ys) != n {
		return 0
	}
	var concordant, discordant, tiesX, tiesY int
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			dx := xs[i] - xs[j]
			dy := ys[i] - ys[j]
			switch {
			case dx == 0 && dy == 0:
				// joint tie contributes to neither concordance set
			case dx == 0:
				tiesX++
			case dy == 0:
				tiesY++
			case (dx > 0) == (dy > 0):
				concordant++
			default:
				discordant++
			}
		}
	}
	n0 := float64(n*(n-1)) / 2
	denom := math.Sqrt((n0 - float64(tiesX)) * (n0 - float64(tiesY)))
	if denom == 0 {
		return 0
	}
	return float64(concordant-discordant) / denom
}

// PairVote is one pairwise judgment against the anchor's implied preference.
type PairVote struct {
	// AnchorABetter is true when A's anchor rating exceeds B's.
	AnchorABetter bool
	// JudgeChoseA is true when the judge preferred A.
	JudgeChoseA bool
}

// PairAgreement returns the fraction of pairs the judge orders the same way
// the anchor does. Ties and inconsistent orderings are excluded by the
// caller, so this is "accuracy on decisive pairs".
func PairAgreement(votes []PairVote) float64 {
	if len(votes) == 0 {
		return 0
	}
	agree := 0
	for _, v := range votes {
		if v.AnchorABetter == v.JudgeChoseA {
			agree++
		}
	}
	return float64(agree) / float64(len(votes))
}

// ranks returns 1-based average ranks for values (ascending). Ties share
// their mean rank, which is what makes the rank correlation tie-aware.
func ranks(values []float64) []float64 {
	n := len(values)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return values[idx[i]] < values[idx[j]] })
	out := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && values[idx[j+1]] == values[idx[i]] {
			j++
		}
		// Average rank of positions i..j (1-based).
		avg := float64((i+1)+(j+1)) / 2
		for k := i; k <= j; k++ {
			out[idx[k]] = avg
		}
		i = j + 1
	}
	return out
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}
