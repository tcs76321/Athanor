package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/llm"
)

type familySelector struct {
	gen, judge string
}

func (f familySelector) Choose(_ context.Context, req ModelRequest) (ModelChoice, bool) {
	if req.Phase == llm.PhaseComparing || req.Phase == llm.PhaseEvaluating {
		return ModelChoice{Model: "judge", Family: f.judge, ContextTarget: 32768}, true
	}
	return ModelChoice{Model: "gen", Family: f.gen, ContextTarget: 32768}, true
}
func (f familySelector) ChooseMany(context.Context, ModelRequest, int) []ModelChoice { return nil }
func (f familySelector) Persona(string) (ModelChoice, bool)                          { return ModelChoice{}, false }
func (f familySelector) Observe(context.Context, string, bool)                       {}
func (f familySelector) PreferPanel() bool                                           { return false }

// TestActualCrossFamilyUsesPoolFamilies proves the acceptance gate reads the
// model families the pool actually selects, not the legacy registry's
// configured roles.
func TestActualCrossFamilyUsesPoolFamilies(t *testing.T) {
	e := newEnv(t)
	projectID, taskID, jobID := e.createProjectTask(t, "text", "Write a short brief about local-first software.")
	p, err := e.projects.Get(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.projects.Task(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}

	e.eng.SetModelSelector(familySelector{gen: "a", judge: "b"})
	gen, judge, ok := e.eng.actualCrossFamily(context.Background(), j, p, task, "main", "security")
	if !ok || gen != "a" || judge != "b" {
		t.Fatalf("cross-family = (%q,%q,%v), want a,b,true", gen, judge, ok)
	}

	e.eng.SetModelSelector(familySelector{gen: "a", judge: "a"})
	if _, _, ok := e.eng.actualCrossFamily(context.Background(), j, p, task, "main", "security"); ok {
		t.Fatal("same-family pool cast reported as cross-family")
	}
}
