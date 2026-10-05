// Pure analytics for the M3-T7 probe. Everything in this file is a
// side-effect-free function over plain values so the measurements are
// unit-testable without a daemon, a model, or a clock. The runner in
// probe.go collects raw rows; these functions turn them into the
// calibration table, the stability classes, and the diversity metric.
package main

import (
	"math"
	"strings"
)

// tokenSet splits s into a set of whitespace-delimited tokens. A simple
// tokenization is deliberate: the Jaccard metric only needs to detect
// "same output / different output" at a coarse granularity, and a
// tokenizer that matched the model's would be both fragile and
// unnecessary for T-a.
func tokenSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, tok := range strings.Fields(s) {
		out[tok] = struct{}{}
	}
	return out
}

// jaccardDistance returns 1 - |A∩B| / |A∪B| over whitespace tokens. Two
// empty texts are treated as identical (distance 0); an empty vs a
// non-empty text is maximally distant (1).
func jaccardDistance(a, b string) float64 {
	A, B := tokenSet(a), tokenSet(b)
	if len(A) == 0 && len(B) == 0 {
		return 0
	}
	inter := 0
	for t := range A {
		if _, ok := B[t]; ok {
			inter++
		}
	}
	union := len(A) + len(B) - inter
	if union == 0 {
		return 0
	}
	return 1 - float64(inter)/float64(union)
}

// avgPairwiseJaccard is the T-a diversity metric: the mean Jaccard
// distance over all unordered pairs of candidate texts. Fewer than two
// candidates has no pair, so it returns 0.
func avgPairwiseJaccard(cands []candidate) float64 {
	if len(cands) < 2 {
		return 0
	}
	var sum float64
	var pairs int
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			sum += jaccardDistance(cands[i].Text, cands[j].Text)
			pairs++
		}
	}
	if pairs == 0 {
		return 0
	}
	return sum / float64(pairs)
}

// confidenceBinLabels are the reliability-diagram bins from the runbook:
// half-open intervals of width 0.1 covering the accept-relevant range.
var confidenceBinLabels = []string{
	"[0.5,0.6)", "[0.6,0.7)", "[0.7,0.8)", "[0.8,0.9)", "[0.9,1.0)",
}

// confidenceBin maps a confidence in [0.5, 1.0] to its bin index, or -1
// when it is outside that range (below the accept threshold, or
// nonsensical). A small epsilon makes the 0.1 boundaries robust to
// float64 representation error (e.g. 0.7*10 == 6.999…).
func confidenceBin(c float64) int {
	if math.IsNaN(c) || c < 0.5 || c > 1 {
		return -1
	}
	if c >= 1 {
		return len(confidenceBinLabels) - 1
	}
	return int(math.Floor(c*10+1e-9)) - 5
}

// confScore is one (judge confidence, observed rubric score) observation
// for the T-b calibration table.
type confScore struct {
	Confidence float64
	Observed   float64
}

// calibrationRow is one reliability-diagram bin: its label, population,
// and the mean observed score of that population. Perfect calibration
// means MeanObserved equals the bin's confidence midpoint.
type calibrationRow struct {
	Label        string
	N            int
	MeanObserved float64
}

// calibrationTable bins (confidence, observed) pairs and returns one row
// per non-empty bin in ascending confidence order. Pairs outside the
// runbook's [0.5, 1.0] range are dropped.
func calibrationTable(pairs []confScore) []calibrationRow {
	counts := make([]int, len(confidenceBinLabels))
	sums := make([]float64, len(confidenceBinLabels))
	for _, p := range pairs {
		b := confidenceBin(p.Confidence)
		if b < 0 {
			continue
		}
		counts[b]++
		sums[b] += p.Observed
	}
	var out []calibrationRow
	for i, label := range confidenceBinLabels {
		if counts[i] == 0 {
			continue
		}
		out = append(out, calibrationRow{
			Label:        label,
			N:            counts[i],
			MeanObserved: sums[i] / float64(counts[i]),
		})
	}
	return out
}

// stabilityClass classifies the winner values from repeated runs. For the
// runbook's 3-run design it returns "3-same", "2-1", or "3-way"; for
// other run counts it returns the generic "all-same" / "majority" /
// "all-distinct". Fewer than two runs is "insufficient".
func stabilityClass(winners []string) string {
	if len(winners) < 2 {
		return "insufficient"
	}
	counts := make(map[string]int, len(winners))
	max := 0
	for _, w := range winners {
		counts[w]++
		if counts[w] > max {
			max = counts[w]
		}
	}
	switch {
	case max == len(winners):
		if len(winners) == 3 {
			return "3-same"
		}
		return "all-same"
	case max >= 2:
		if len(winners) == 3 {
			return "2-1"
		}
		return "majority"
	default:
		if len(winners) == 3 {
			return "3-way"
		}
		return "all-distinct"
	}
}

// mean returns the arithmetic mean of xs, or 0 for an empty slice.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
