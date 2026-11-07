package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
)

type fakeClarifier struct {
	calls int
	text  string
	job   string
	phase string
}

func (f *fakeClarifier) Ask(_ context.Context, jobID, _, phase, question string) error {
	f.calls++
	f.job = jobID
	f.phase = phase
	f.text = question
	return nil
}

func TestParseQuestion(t *testing.T) {
	cases := map[string]string{
		"Plan.\nQUESTION: Which database?\nDone.": "Which database?",
		"QUESTION:   spaced  ":                    "spaced",
		"A question, not a marker.":               "",
		"QUESTION:\n":                             "",
	}
	for input, want := range cases {
		if got := parseQuestion(input); got != want {
			t.Errorf("parseQuestion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPlanningQuestionParksTheJob(t *testing.T) {
	e := newEnv(t)
	clarifier := &fakeClarifier{}
	e.eng.SetClarifier(clarifier)
	on := true
	e.cfg.Execution.Policy.Clarifications = &on
	e.ollama.setPlanning("I need one fact first.\nQUESTION: Which database should this target?")

	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	if clarifier.calls != 1 {
		t.Fatalf("clarifier calls = %d, want 1", clarifier.calls)
	}
	if clarifier.phase != "planning" || clarifier.text != "Which database should this target?" {
		t.Fatalf("clarifier = %+v", clarifier)
	}
	if clarifier.job != jobID {
		t.Fatalf("clarifier job = %q, want %q", clarifier.job, jobID)
	}
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StatePlanning {
		t.Fatalf("job state = %s, want planning (parked by the caller's HITL seam)", j.State)
	}
}

func TestQuestionWithoutClarifierDoesNotPark(t *testing.T) {
	e := newEnv(t)
	e.ollama.setPlanning("Plan.\nQUESTION: Which database?")
	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("job state = %s, want completed (no clarifier wired)", j.State)
	}
}
