package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/strategy"
)

type fakeArmHistory struct {
	stats []strategy.CandidateStat
}

func (f fakeArmHistory) RecentStats(context.Context, string, int) (int, float64, error) {
	return 0, 0, nil
}
func (f fakeArmHistory) CandidateStats(context.Context, string, int) ([]strategy.CandidateStat, error) {
	return f.stats, nil
}

func TestApplyLearnedSelectionChoosesStrongArm(t *testing.T) {
	e := newEnv(t)
	e.eng.SetStrategyHistory(fakeArmHistory{stats: []strategy.CandidateStat{
		{Candidates: 3, Samples: 20, Accepted: 2},
		{Candidates: 5, Samples: 20, Accepted: 18},
	}})
	plan := policy.Plan{Candidates: 3}
	in := policy.Inputs{
		Features: policy.Features{Archetype: "text", JobKey: "job-1", Attempt: 1},
		Limits:   policy.Limits{CandidatesCeiling: 5},
	}
	e.eng.applyLearnedSelection(context.Background(), in, &plan)
	if plan.Candidates != 5 {
		t.Fatalf("candidates = %d, want the strong arm 5", plan.Candidates)
	}
}

func TestApplyLearnedSelectionSkipsRetry(t *testing.T) {
	e := newEnv(t)
	e.eng.SetStrategyHistory(fakeArmHistory{stats: []strategy.CandidateStat{
		{Candidates: 3, Samples: 20, Accepted: 2},
		{Candidates: 5, Samples: 20, Accepted: 18},
	}})
	plan := policy.Plan{Candidates: 3}
	in := policy.Inputs{
		Features: policy.Features{Archetype: "text", JobKey: "job-1", Attempt: 2},
		Limits:   policy.Limits{CandidatesCeiling: 5},
	}
	e.eng.applyLearnedSelection(context.Background(), in, &plan)
	if plan.Candidates != 3 {
		t.Fatalf("retry candidates = %d, want 3 (the escalation ladder owns retries)", plan.Candidates)
	}
}

func TestApplyLearnedSelectionDisabled(t *testing.T) {
	e := newEnv(t)
	e.eng.SetStrategyHistory(fakeArmHistory{stats: []strategy.CandidateStat{
		{Candidates: 3, Samples: 20, Accepted: 2},
		{Candidates: 5, Samples: 20, Accepted: 18},
	}})
	off := false
	e.cfg.Execution.Policy.LearnedSelection = &off
	plan := policy.Plan{Candidates: 3}
	in := policy.Inputs{
		Features: policy.Features{Archetype: "text", JobKey: "job-1", Attempt: 1},
		Limits:   policy.Limits{CandidatesCeiling: 5},
	}
	e.eng.applyLearnedSelection(context.Background(), in, &plan)
	if plan.Candidates != 3 {
		t.Fatalf("candidates = %d, want 3 when learned selection is off", plan.Candidates)
	}
}

func TestApplyLearnedSelectionNoHistory(t *testing.T) {
	e := newEnv(t)
	plan := policy.Plan{Candidates: 3}
	in := policy.Inputs{
		Features: policy.Features{Archetype: "text", JobKey: "job-1", Attempt: 1},
		Limits:   policy.Limits{CandidatesCeiling: 5},
	}
	e.eng.applyLearnedSelection(context.Background(), in, &plan)
	if plan.Candidates != 3 {
		t.Fatalf("candidates = %d, want fallback 3 without history", plan.Candidates)
	}
}
