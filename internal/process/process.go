// Package process evaluates a whole job trajectory, not just its final
// artifact (ADR-0119). It answers the questions a per-artifact score cannot:
// was the extra compute necessary, did escalation rescue the job, did the
// full loop pay for a panel it never needed, and did the final artifact fall
// below the best candidate already seen?
//
// The evaluation is pure and deterministic. The engine fills the input from
// the notebook, event log, and evaluation records at terminal state; the
// scorecard is written as a `process_eval` audit row.
package process

import (
	"fmt"
	"time"
)

// Input is the observed trajectory of one job.
type Input struct {
	// FinalAccepted is true when the job delivered an accepted artifact.
	FinalAccepted bool
	// Candidates is the total number of proposals generated.
	Candidates int
	// Cycles is the number of divergence cycles (1 = one pass).
	Cycles int
	// Repairs, Reflections, Replans, Observations are mechanism counts.
	Repairs      int
	Reflections  int
	Replans      int
	Observations int
	// MemoryHits is the number of retrieved memory items injected.
	MemoryHits int
	// Tokens and Wall are the job's total cost.
	Tokens int
	Wall   time.Duration
	// FinalScore and BestCandidateScore are the evaluation scores.
	FinalScore         float64
	BestCandidateScore float64
}

// Scorecard is the process-level judgment.
type Scorecard struct {
	// OneShotSufficient: an accepted single-candidate job with no escalation.
	OneShotSufficient bool
	// Escalated: the job used more than one cycle or a repair/reflection/
	// replan/observation.
	Escalated bool
	// Rescued: escalation ended in an accepted artifact.
	Rescued bool
	// WastedCompute: the panel ran more than one candidate but no escalation
	// was needed.
	WastedCompute bool
	// Regression: the delivered final scored below the best candidate.
	Regression bool
	// Verdict is a compact human-readable label.
	Verdict string
}

// Evaluate applies the process rules. It is total and pure.
func Evaluate(in Input) Scorecard {
	escalated := in.Cycles > 1 || in.Repairs > 0 || in.Reflections > 0 ||
		in.Replans > 0 || in.Observations > 0
	out := Scorecard{Escalated: escalated}
	out.OneShotSufficient = in.FinalAccepted && in.Candidates <= 1 && !escalated
	out.Rescued = in.FinalAccepted && escalated
	out.WastedCompute = in.FinalAccepted && !escalated && in.Candidates > 1
	out.Regression = in.FinalAccepted && in.BestCandidateScore > in.FinalScore+1e-9

	switch {
	case out.Regression:
		out.Verdict = "quality regression: final is below the best candidate"
	case out.Rescued:
		out.Verdict = "escalation rescued the job"
	case out.OneShotSufficient:
		out.Verdict = "one-shot sufficient"
	case out.WastedCompute:
		out.Verdict = "panel sufficient, no escalation needed"
	case in.FinalAccepted:
		out.Verdict = "accepted"
	case escalated:
		out.Verdict = "failed after escalation"
	default:
		out.Verdict = "failed without escalation"
	}
	if out.WastedCompute && in.Tokens > 0 {
		out.Verdict = fmt.Sprintf("%s (%d tokens spent on unused candidates)", out.Verdict, in.Tokens)
	}
	return out
}
