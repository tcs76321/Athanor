package policy

import (
	"fmt"
	"math"
	"sort"
)

// ArmStat is one observed candidate-count arm for a task class. It is the
// input to the UCB selector: how often the arm ran and how often it produced
// an accepted-new outcome.
type ArmStat struct {
	Candidates int
	Samples    int
	Accepted   int
}

// LearnedSelectionMinSamples is the minimum number of observations an arm
// needs before it can beat the fallback. Below it the fallback is kept, so a
// two-sample fluke cannot reshape the loop.
const LearnedSelectionMinSamples = 8

// ChooseCandidates applies a UCB1-style rule to the observed candidate arms.
// It is pure and deterministic: ties break toward the fallback, then toward
// the smaller arm. The fallback is always the safe baseline; an arm is only
// chosen when it has enough evidence and its optimistic score beats the
// fallback's optimistic score.
//
// The exploration bonus is sqrt(2 ln(total) / n), so an under-sampled arm
// remains eligible even after a strong arm emerges. This is the "selection,
// not repertoire" rule: the arm set is fixed and the engine never prunes it.
func ChooseCandidates(arms []ArmStat, fallback, ceiling int, minSamples int) (int, string) {
	if fallback < 1 {
		fallback = 1
	}
	if ceiling < fallback {
		ceiling = fallback
	}
	if minSamples <= 0 {
		minSamples = LearnedSelectionMinSamples
	}
	// Clamp and dedupe by candidate count.
	byCount := map[int]ArmStat{}
	total := 0
	for _, arm := range arms {
		if arm.Candidates < 1 || arm.Candidates > ceiling || arm.Samples <= 0 {
			continue
		}
		cur := byCount[arm.Candidates]
		cur.Candidates = arm.Candidates
		cur.Samples += arm.Samples
		cur.Accepted += arm.Accepted
		byCount[arm.Candidates] = cur
		total += arm.Samples
	}
	if total == 0 {
		return fallback, "no history; fallback"
	}
	fallbackArm, hasFallback := byCount[fallback]
	if !hasFallback || fallbackArm.Samples < minSamples {
		return fallback, fmt.Sprintf("fallback arm has %d/%d samples; fallback", fallbackArm.Samples, minSamples)
	}
	fallbackScore := ucbScore(fallbackArm, total)
	best := fallback
	bestScore := fallbackScore
	var bestArm ArmStat
	// Deterministic order: larger candidate counts first only when they are
	// clearly better; ties keep the smaller/fallback arm.
	counts := make([]int, 0, len(byCount))
	for count := range byCount {
		counts = append(counts, count)
	}
	sort.Ints(counts)
	for _, count := range counts {
		arm := byCount[count]
		if arm.Samples < minSamples {
			continue
		}
		score := ucbScore(arm, total)
		if score > bestScore+1e-9 {
			best, bestScore, bestArm = count, score, arm
		}
	}
	reason := fmt.Sprintf("evidence selected %d candidates (%.2f accept, %d samples) over fallback %d (%.2f)",
		best, armRate(bestArm), bestArm.Samples, fallback, armRate(fallbackArm))
	if best == fallback {
		reason = fmt.Sprintf("fallback %d retained (%.2f accept, %d samples)", fallback, armRate(fallbackArm), fallbackArm.Samples)
	}
	return best, reason
}

func ucbScore(arm ArmStat, total int) float64 {
	if arm.Samples <= 0 || total <= 0 {
		return 0
	}
	return armRate(arm) + math.Sqrt(2*math.Log(float64(total))/float64(arm.Samples))
}

func armRate(arm ArmStat) float64 {
	if arm.Samples <= 0 {
		return 0
	}
	return float64(arm.Accepted) / float64(arm.Samples)
}
