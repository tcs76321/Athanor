package engine

import (
	"context"
	"strings"

	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// pairwiseVerdict is the judge's response to an A/B comparison. It is a
// separate shape from the pointwise acceptance.Verdict because the judge is
// naming a position, not an artifact role.
type pairwiseVerdict struct {
	Winner     string   `json:"winner"` // "A" | "B" | "tie"
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons"`
}

const pairwiseInstructions = `PAIRWISE COMPARISON. Candidate A and Candidate B are two artifacts for the same task.
Decide which one better satisfies the task and every acceptance criterion.
Judge substance, not position: do not prefer A because it is first, and do not reward length or confident prose.
Output one JSON object only:
{"winner":"A|B|tie","confidence":0.0-1.0,"reasons":["..."]}
Use "tie" only when the artifacts are materially equivalent for this task.`

// runPairwiseJudge asks the same judge both orderings of the same pair. The
// position-bias control is the point: a judge that always says "A" produces
// two contradictory answers and cannot decide. Returns decided=false when the
// orders disagree or a call fails, so the caller can fall back to the pointwise
// judge rather than trusting a biased result.
func (e *Engine) runPairwiseJudge(ctx context.Context, j job.Job, p project.Project, t project.Task,
	judgeRole, current, previous string) (acceptance.Verdict, bool, error) {

	current = truncateForLimit(current, comparisonContentLimit)
	previous = truncateForLimit(previous, comparisonContentLimit)

	// Order 1: A=current, B=previous. Order 2: A=previous, B=current.
	first, firstOK, err := e.pairwiseCall(ctx, j, p, t, judgeRole, current, previous, 0)
	if err != nil {
		return acceptance.Verdict{}, false, err
	}
	second, secondOK, err := e.pairwiseCall(ctx, j, p, t, judgeRole, previous, current, 1)
	if err != nil {
		return acceptance.Verdict{}, false, err
	}
	if !firstOK || !secondOK {
		e.audit(ctx, j.ID, map[string]any{
			"event": "pairwise_judge_fallback", "reason": "unparseable order",
			"first_ok": firstOK, "second_ok": secondOK,
		})
		return acceptance.Verdict{}, false, nil
	}

	// Map each position verdict to the artifact it names.
	firstChoice := acceptance.PairwiseChoice(first.Winner, "new", "previous")
	secondChoice := acceptance.PairwiseChoice(second.Winner, "previous", "new")
	if firstChoice == "" || secondChoice == "" {
		e.audit(ctx, j.ID, map[string]any{
			"event": "pairwise_judge_fallback", "reason": "unknown position",
			"first": first.Winner, "second": second.Winner,
		})
		return acceptance.Verdict{}, false, nil
	}
	e.audit(ctx, j.ID, map[string]any{
		"event": "pairwise_judge", "first_order": first.Winner, "second_order": second.Winner,
		"first_choice": firstChoice, "second_choice": secondChoice,
	})
	if firstChoice != secondChoice {
		e.audit(ctx, j.ID, map[string]any{
			"event": "judge_position_bias", "first_choice": firstChoice,
			"second_choice": secondChoice, "resolution": "fallback to pointwise",
		})
		return acceptance.Verdict{}, false, nil
	}
	winner := firstChoice
	if winner == "tie" {
		// A tie cannot accept new work; the conservative outcome is previous.
		winner = "previous"
	}
	reasons := append([]string{}, first.Reasons...)
	reasons = append(reasons, second.Reasons...)
	return acceptance.Verdict{
		Winner: winner, Confidence: 1,
		Reasons: append([]string{"pairwise judge agreed in both orders"}, reasons...),
	}, true, nil
}

// pairwiseCall runs one ordering. A=trueContent, B=falseContent.
func (e *Engine) pairwiseCall(ctx context.Context, j job.Job, p project.Project, t project.Task,
	judgeRole, aContent, bContent string, voteIndex int) (pairwiseVerdict, bool, error) {

	resp, err := e.call(ctx, j, p, t, llm.PhaseComparing, judgeRole, pairwiseInstructions,
		[]prompt.CandidateArtifact{
			{Kind: "a", Content: aContent},
			{Kind: "b", Content: bContent},
		}, withSeedSalt(100+voteIndex))
	if err != nil {
		return pairwiseVerdict{}, false, err
	}
	v, coercions, err := acceptance.ParseVerdictCoerced[pairwiseVerdict](resp.Content)
	if err != nil {
		e.audit(ctx, j.ID, map[string]any{
			"event": "pairwise_verdict_unparsed", "vote": voteIndex, "error": err.Error(),
		})
		return pairwiseVerdict{}, false, nil
	}
	if len(coercions) > 0 {
		e.audit(ctx, j.ID, map[string]any{
			"event": "verdict_coerced", "phase": string(llm.PhaseComparing), "fields": coercions,
		})
	}
	v.Winner = strings.ToUpper(strings.TrimSpace(v.Winner))
	switch v.Winner {
	case "A", "B", "TIE":
		return v, true, nil
	default:
		return pairwiseVerdict{}, false, nil
	}
}
