package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/policy"
)

// TestPlanEscalatesOnRetry pins ADR-0074 §3 through the engine: a task's second
// job (a retry) draws a larger compute plan than the first, bounded by the hard
// ceiling — while the first attempt is left unescalated.
func TestPlanEscalatesOnRetry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID, taskID, jobID := e.createProjectTask(t, "text", "Write a short essay about local-first software.")

	planFor := func(id string) policy.Plan {
		j, err := e.jobs.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.projects.Get(ctx, projectID)
		if err != nil {
			t.Fatal(err)
		}
		task, err := e.projects.Task(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		return e.eng.planFor(ctx, j, p, task)
	}

	first := planFor(jobID)
	if first.Candidates != e.cfg.Execution.DivergenceCandidates {
		t.Errorf("first-attempt candidates = %d, want the configured %d (no escalation)",
			first.Candidates, e.cfg.Execution.DivergenceCandidates)
	}

	j2, err := e.jobs.Create(ctx, taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	second := planFor(j2.ID)

	if second.Candidates <= first.Candidates {
		t.Errorf("attempt 2 candidates = %d, want more than attempt 1 (%d)", second.Candidates, first.Candidates)
	}
	if hard := e.cfg.Execution.MaxHardTaskVariations; hard > 0 && second.Candidates > hard {
		t.Errorf("attempt 2 candidates = %d exceeds the hard ceiling %d", second.Candidates, hard)
	}
}
