package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/policy"
)

// TestPlanEscalatesOnRetry pins ADR-0074 §3 through the engine: a task's retry
// draws a larger plan, while the first attempt is left unescalated.
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
	if first.Candidates != e.cfg.Execution.DivergenceCandidates || first.BudgetScale != 0 {
		t.Errorf("first attempt = candidates %d scale %v, want %d/0 (no escalation)",
			first.Candidates, first.BudgetScale, e.cfg.Execution.DivergenceCandidates)
	}

	j2, err := e.jobs.Create(ctx, taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	second := planFor(j2.ID)
	if second.BudgetScale <= 1 {
		t.Errorf("attempt 2 budget scale = %v, want > 1 (the budget rung)", second.BudgetScale)
	}

	j3, err := e.jobs.Create(ctx, taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	third := planFor(j3.ID)
	if third.Candidates <= second.Candidates {
		t.Errorf("attempt 3 candidates = %d, want more than attempt 2 (%d)", third.Candidates, second.Candidates)
	}
	if hard := e.cfg.Execution.MaxHardTaskVariations; hard > 0 && third.Candidates > hard {
		t.Errorf("attempt 3 candidates = %d exceeds the hard ceiling %d", third.Candidates, hard)
	}
}

func TestPlanBudget(t *testing.T) {
	base := 10 * time.Minute
	if got := planBudget(base, policy.Plan{BudgetScale: 1}); got != base {
		t.Errorf("scale 1 changed the budget to %v", got)
	}
	if got := planBudget(base, policy.Plan{BudgetScale: 2}); got != 20*time.Minute {
		t.Errorf("scale 2 = %v, want 20m", got)
	}
}

func TestPlanTemperature(t *testing.T) {
	if got := planTemperature(0.5, policy.Plan{TemperatureDelta: 0.25}, false); got != 0.75 {
		t.Errorf("generation delta = %v, want 0.75", got)
	}
	if got := planTemperature(0.5, policy.Plan{TemperatureDelta: 0.25}, true); got != 0.5 {
		t.Errorf("judgment phase applied the delta: %v, want 0.5", got)
	}
	if got := planTemperature(0.95, policy.Plan{TemperatureDelta: 0.25}, false); got != 1 {
		t.Errorf("clamp = %v, want 1", got)
	}
}

func TestPlanInstructions(t *testing.T) {
	if got := planInstructions("rubric", policy.Plan{Directive: "x"}, true); got != "rubric" {
		t.Errorf("judgment phase applied the directive: %q", got)
	}
	if got := planInstructions("base", policy.Plan{Directive: "retry"}, false); got != "base\nretry" {
		t.Errorf("generation directive = %q, want base\\nretry", got)
	}
	if got := planInstructions("", policy.Plan{Directive: "retry"}, false); got != "retry" {
		t.Errorf("empty base = %q, want retry", got)
	}
}
