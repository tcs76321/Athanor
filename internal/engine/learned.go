package engine

import (
	"context"
	"log/slog"

	"github.com/tcs76321/athanor/internal/policy"
)

// applyLearnedSelection lets observed outcomes adjust the candidate count for
// a first attempt (ADR-0110). It is deliberately narrow: evidence can lower or
// confirm the count, never bypass the planner's hard-task raise or the
// operator ceiling. A retry keeps the escalation ladder unchanged.
func (e *Engine) applyLearnedSelection(ctx context.Context, in policy.Inputs, plan *policy.Plan) {
	if e.cfg == nil || !e.cfg.Execution.LearnedSelectionEnabled() {
		return
	}
	if in.Features.Attempt > 1 {
		return
	}
	hist, ok := e.history.(StrategyArmHistory)
	if !ok {
		return
	}
	stats, err := hist.CandidateStats(ctx, in.Features.Archetype, 200)
	if err != nil {
		slog.Warn("engine: reading candidate arm history", "archetype", in.Features.Archetype, "err", err)
		return
	}
	arms := make([]policy.ArmStat, 0, len(stats))
	for _, stat := range stats {
		arms = append(arms, policy.ArmStat{
			Candidates: stat.Candidates, Samples: stat.Samples, Accepted: stat.Accepted,
		})
	}
	ceiling := plan.Candidates
	if in.Limits.CandidatesCeiling > ceiling {
		ceiling = in.Limits.CandidatesCeiling
	}
	choice, reason := policy.ChooseCandidates(arms, plan.Candidates, ceiling, policy.LearnedSelectionMinSamples)
	if choice == plan.Candidates {
		return
	}
	e.audit(ctx, in.Features.JobKey, map[string]any{
		"event":       "learned_candidate_selection",
		"archetype":   in.Features.Archetype,
		"from":        plan.Candidates,
		"to":          choice,
		"reason":      reason,
		"arm_samples": len(arms),
	})
	plan.Candidates = choice
}
