package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/notebook"
)

func TestObserveForRepairReadsOneFileIntoNotebook(t *testing.T) {
	e := newEnv(t)
	nb := notebook.New(e.db)
	e.eng.SetNotebook(nb)
	e.ollama.setGeneric(`{"read_file":"repo/main.py"}`)

	projectID, taskID, jobID := e.createProjectTask(t, "code",
		"Fix the failing Python module in the repository.")
	j, _ := e.jobs.Get(context.Background(), jobID)
	p, _ := e.projects.Get(context.Background(), projectID)
	task, _ := e.projects.Task(context.Background(), taskID)

	e.eng.phaseDeps.ObserveForRepair(context.Background(), j, p, task, "main")

	calls := e.runner.Calls()
	found := false
	for _, call := range calls {
		if call.Tool == "ReadFile" && call.Cmd == "repo/main.py" {
			found = true
		}
	}
	if !found {
		t.Fatalf("read_file was not dispatched: %+v", calls)
	}
	entries, err := nb.Recent(context.Background(), jobID, 10)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, entry := range entries {
		if entry.Kind == notebook.KindObservation && strings.Contains(entry.Content, "def add") {
			found = true
		}
	}
	if !found {
		t.Fatalf("observation missing from notebook: %+v", entries)
	}
}

func TestObserveForRepairNoFile(t *testing.T) {
	e := newEnv(t)
	nb := notebook.New(e.db)
	e.eng.SetNotebook(nb)
	e.ollama.setGeneric(`{"read_file":null}`)
	projectID, taskID, jobID := e.createProjectTask(t, "code",
		"Fix the failing Python module in the repository.")
	j, _ := e.jobs.Get(context.Background(), jobID)
	p, _ := e.projects.Get(context.Background(), projectID)
	task, _ := e.projects.Task(context.Background(), taskID)

	e.eng.phaseDeps.ObserveForRepair(context.Background(), j, p, task, "main")
	for _, call := range e.runner.Calls() {
		if call.Tool == "ReadFile" {
			t.Fatalf("read_file ran for a null request: %+v", call)
		}
	}
}
