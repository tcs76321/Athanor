package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/store"
)

type fakeContextPlanner struct {
	calls int
	err   error
}

func (f *fakeContextPlanner) PlanContext(req ContextRequest) (ContextDecision, error) {
	f.calls++
	if f.err != nil {
		return ContextDecision{}, f.err
	}
	if req.ModelMax <= 0 {
		return ContextDecision{}, errors.New("model max missing")
	}
	return ContextDecision{
		Target: 8192, Bucket: 8192, Floor: 4096,
		Demand: req.Demand, Binding: "fake planner",
	}, nil
}

func TestContextPlannerSetsWindowAndAudits(t *testing.T) {
	env := newEnv(t)
	fp := &fakeContextPlanner{}
	env.eng.SetContextPlanner(fp)
	jobID := env.submit(t)
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed", j.State)
	}
	if fp.calls == 0 {
		t.Fatal("planner was never called")
	}
	events, err := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.DataJSON, `"event":"context_plan"`) {
			found = true
			if !strings.Contains(e.DataJSON, `"planned":true`) {
				t.Errorf("context_plan = %s, want planned:true", e.DataJSON)
			}
		}
	}
	if !found {
		t.Fatal("no context_plan audit row")
	}
}

func TestContextPlannerFailurePausesJob(t *testing.T) {
	env := newEnv(t)
	env.eng.SetContextPlanner(&fakeContextPlanner{err: errors.New("no window")})
	jobID := env.submit(t)
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StatePaused {
		t.Fatalf("state = %s, want paused", j.State)
	}
	events, err := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.DataJSON, `"event":"context_plan_failed"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("no context_plan_failed audit row")
	}
}
