package engine

import (
	"context"
	"testing"
)

func pairwiseEnv(t *testing.T) (*testEnv, string, string, string) {
	t.Helper()
	e := newEnv(t)
	projectID, taskID, jobID := e.createProjectTask(t, "document",
		"Write a short brief that satisfies the criteria.")
	return e, projectID, taskID, jobID
}

func TestPairwiseJudgeAgreesNew(t *testing.T) {
	e, projectID, taskID, jobID := pairwiseEnv(t)
	e.ollama.comparisonVerdicts = []string{
		`{"winner":"A","confidence":0.9,"reasons":["A is clearer"]}`,
		`{"winner":"B","confidence":0.9,"reasons":["B is clearer"]}`,
	}
	j, _ := e.jobs.Get(context.Background(), jobID)
	p, _ := e.projects.Get(context.Background(), projectID)
	task, _ := e.projects.Task(context.Background(), taskID)
	got, ok, err := e.eng.phaseDeps.RunPairwiseJudge(context.Background(), j, p, task, "security", "new", "old")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Winner != "new" {
		t.Fatalf("got %+v ok=%v, want new", got, ok)
	}
}

func TestPairwiseJudgeAgreesPrevious(t *testing.T) {
	e, projectID, taskID, jobID := pairwiseEnv(t)
	e.ollama.comparisonVerdicts = []string{
		`{"winner":"B","confidence":0.9,"reasons":["B is better"]}`,
		`{"winner":"A","confidence":0.9,"reasons":["A is better"]}`,
	}
	j, _ := e.jobs.Get(context.Background(), jobID)
	p, _ := e.projects.Get(context.Background(), projectID)
	task, _ := e.projects.Task(context.Background(), taskID)
	got, ok, err := e.eng.phaseDeps.RunPairwiseJudge(context.Background(), j, p, task, "security", "new", "old")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Winner != "previous" {
		t.Fatalf("got %+v ok=%v, want previous", got, ok)
	}
}

func TestPairwiseJudgeDetectsPositionBias(t *testing.T) {
	e, projectID, taskID, jobID := pairwiseEnv(t)
	// Always "A": the first order says new, the second says previous.
	e.ollama.comparisonVerdicts = []string{
		`{"winner":"A","confidence":0.9,"reasons":[]}`,
		`{"winner":"A","confidence":0.9,"reasons":[]}`,
	}
	j, _ := e.jobs.Get(context.Background(), jobID)
	p, _ := e.projects.Get(context.Background(), projectID)
	task, _ := e.projects.Task(context.Background(), taskID)
	got, ok, err := e.eng.phaseDeps.RunPairwiseJudge(context.Background(), j, p, task, "security", "new", "old")
	if err != nil {
		t.Fatal(err)
	}
	if ok || got.Winner != "" {
		t.Fatalf("position-biased verdict was accepted: %+v ok=%v", got, ok)
	}
}

func TestPairwiseJudgeRejectsUnknownPosition(t *testing.T) {
	e, projectID, taskID, jobID := pairwiseEnv(t)
	e.ollama.comparisonVerdicts = []string{
		`{"winner":"new","confidence":0.9,"reasons":[]}`,
		`{"winner":"new","confidence":0.9,"reasons":[]}`,
	}
	j, _ := e.jobs.Get(context.Background(), jobID)
	p, _ := e.projects.Get(context.Background(), projectID)
	task, _ := e.projects.Task(context.Background(), taskID)
	_, ok, err := e.eng.phaseDeps.RunPairwiseJudge(context.Background(), j, p, task, "security", "new", "old")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a pointwise winner value was accepted as a pairwise position")
	}
}
