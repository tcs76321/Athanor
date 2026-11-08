// Acceptance guards and record composition.
//
// Every rule here is a pure decision over evidence the caller already
// gathered. The phase package supplies the LLM verdict, the deterministic
// verifier result, and the previous artifact's history; these functions decide
// what the engine may accept.
package acceptance

import (
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/verify"
)

// LoserWinner is the winner to record when the new artifact cannot be
// accepted: the previous artifact if one exists, otherwise none.
func LoserWinner(hasPrevious bool) string {
	if hasPrevious {
		return "previous"
	}
	return "none"
}

// FinalRecord constructs the EvaluationRecord for the synthesized final
// artifact from the deterministic verifier result (known-issues D1).
//
// BetterThanPrevious is set only when the comparison is deterministic: a hard
// verifier passed AND its score is at least the previous accepted artifact's
// average score. That is §19.3's "tests_pass_better_or_equal" applied without
// asking the LLM to self-report an unreliable flag (ADR-0043).
func FinalRecord(j job.Job, final artifact.Artifact, previousID string,
	previousAvgScore float64, ver verify.Result) evaluation.Record {

	rec := evaluation.NewRecord(j.ID, final.ID)
	rec.ComparedAgainst = previousID
	rec.Score = ver.Score
	if rec.Score > 1 {
		rec.Score = 1
	}
	rec.PassedTests = ver.Passed
	if !ver.Passed {
		rec.MissingCriteria = ver.Reasons
	}
	if previousID != "" && previousAvgScore > 0 && ver.HardDecisive() &&
		ver.HardPassed && rec.Score >= previousAvgScore {
		rec.BetterThanPrevious = true
	}
	if ver.HardDecisive() {
		rec.Confidence = 1
	}
	rec.Summary = "deterministic verifier on the synthesized final artifact"
	return rec
}

// ResolveRewardHack applies the F4-T4 reward-hacking guard: when an objective
// verifier decisively failed but the verdict would accept the new artifact, the
// verifier wins. It returns the (possibly rewritten) verdict and whether an
// override occurred.
func ResolveRewardHack(verdict Verdict, ver verify.Result, hasPrevious bool) (Verdict, bool) {
	if !ver.HardDecisive() || ver.HardPassed || verdict.Winner != "new" {
		return verdict, false
	}
	verdict.Winner = LoserWinner(hasPrevious)
	verdict.Confidence = 1
	verdict.Reasons = append(verdict.Reasons,
		"reward-hacking guard: deterministic verifier failed; LLM 'new' overridden")
	return verdict, true
}

// CostAwareEligible reports whether the F4-T6 cost tie-break may run for this
// comparison. A decisive hard-verifier failure is authoritative: no cost tie
// may overturn it. The previous side must also have a known score.
func CostAwareEligible(ver verify.Result, finalRecord evaluation.Record, previousAvgScore float64) bool {
	if ver.HardDecisive() && !ver.HardPassed {
		return false
	}
	if previousAvgScore <= 0 {
		return false
	}
	if finalRecord.Score <= 0 {
		return false
	}
	return true
}

// CostTieWins reports whether the new artifact wins a near-tie on quality by
// spending fewer tokens (F4-T6). A zero cost is unknown and never wins.
func CostTieWins(newScore, prevScore, margin float64, newTokens, prevTokens int) bool {
	if newTokens <= 0 || prevTokens <= 0 {
		return false
	}
	return newScore >= prevScore-margin && newTokens < prevTokens
}

// ShouldFallbackToCandidate implements the ADR-0117 monotonicity rule: a
// synthesis rewrite must never be worse than its source candidate. A passing
// candidate is always kept on a final hard failure; a non-passing candidate is
// kept only when it also passed or scored higher than the rewrite.
func ShouldFallbackToCandidate(wasPassing bool, final, candidate verify.Result) bool {
	if wasPassing {
		return true
	}
	if !final.HardDecisive() || final.HardPassed {
		return false
	}
	if candidate.HardDecisive() && candidate.HardPassed {
		return true
	}
	return candidate.Score > final.Score
}

// MajorityWinner returns the winner with a strict majority of votes, or "" when
// there is no majority.
func MajorityWinner(votes []string) string {
	if len(votes) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, v := range votes {
		counts[v]++
	}
	for _, w := range []string{"new", "previous", "none"} {
		if counts[w]*2 > len(votes) {
			return w
		}
	}
	return ""
}

// PairwiseChoice maps a position verdict to an artifact role. first and second
// are the roles named by A and B respectively.
func PairwiseChoice(winner, first, second string) string {
	switch winner {
	case "A":
		return first
	case "B":
		return second
	case "TIE":
		return "tie"
	default:
		return ""
	}
}

// ParseComparisonVerdict parses the comparison-phase JSON. The winner is
// trimmed and checked against the closed set; an unknown winner is reported as
// ErrUnknownWinner so the caller can audit the safe downgrade to "none".
func ParseComparisonVerdict(content string) (Verdict, []string, error) {
	v, coercions, err := ParseVerdictCoerced[Verdict](content)
	if err != nil {
		return v, coercions, err
	}
	v.Winner = strings.TrimSpace(v.Winner)
	switch v.Winner {
	case "new", "previous", "none":
		return v, coercions, nil
	default:
		return v, coercions, fmt.Errorf("winner %q: %w", v.Winner, ErrUnknownWinner)
	}
}
