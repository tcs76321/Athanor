package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// TestSynthesizeFallsBackForDeclaredChecks pins the P0 extension of ADR-0068:
// a text candidate that passed its declared checks must not be discarded when
// the synthesis rewrite fails them. Before this, only code had the fallback,
// so a text job could fail even though a verified candidate existed.
func TestSynthesizeFallsBackForDeclaredChecks(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	projectID, _, jobID := env.createProjectTask(t, "text",
		"Rewrite the passage while preserving the required budget language.")
	if err := env.projects.SetExecution(ctx, projectID, project.Execution{
		Checks: &project.ExecutionChecks{MustContain: []string{"budget"}},
	}); err != nil {
		t.Fatal(err)
	}

	passing := "The budget is preserved in this rewrite."
	env.ollama.setGeneric(passing)
	env.ollama.setSynthesis("A rewrite that dropped the required word.")

	env.eng.Run(ctx, jobID)

	j, err := env.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed via synthesis fallback", j.State)
	}
	final, err := env.artifacts.LatestForJob(ctx, jobID, artifact.KindDocument)
	if err != nil {
		t.Fatalf("loading final artifact: %v", err)
	}
	content, err := env.artifacts.ReadContent(ctx, final.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != passing {
		t.Errorf("final content = %q, want the passing candidate %q", content, passing)
	}
	events, err := env.db.QueryEvents(ctx, store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.DataJSON, "synthesis_fallback_to_candidate") {
			found = true
			break
		}
	}
	if !found {
		t.Error("synthesis fallback was not audited")
	}
}
