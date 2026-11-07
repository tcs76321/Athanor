package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/notebook"
)

// TestNotebookReachesLaterPhases pins ADR-0109: the plan and evaluation
// results persist and are visible to later model calls.
func TestNotebookReachesLaterPhases(t *testing.T) {
	e := newEnv(t)
	nb := notebook.New(e.db)
	e.eng.SetNotebook(nb)

	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	prompts := e.ollama.promptTexts()
	if len(prompts) == 0 {
		t.Fatal("no model call recorded")
	}
	found := false
	for _, prompt := range prompts {
		if strings.Contains(prompt, "WORKING NOTES") && strings.Contains(prompt, "planning") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("notebook did not reach a later prompt")
	}

	entries, err := nb.Recent(context.Background(), jobID, 50)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[notebook.Kind]bool{}
	for _, entry := range entries {
		kinds[entry.Kind] = true
	}
	for _, want := range []notebook.Kind{notebook.KindPlan, notebook.KindResult, notebook.KindDecision} {
		if !kinds[want] {
			t.Errorf("notebook is missing a %q entry: %+v", want, kinds)
		}
	}
}
