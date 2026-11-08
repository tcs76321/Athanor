package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// TestFinalizeCapturesReflectionCandidatesAndRetries pins the evidence order:
// the outcome is written before scratch cleanup, the actual first-cycle
// candidate count comes from the audit stream, and task retries are derived
// from the job set rather than left at zero.
func TestFinalizeCapturesReflectionCandidatesAndRetries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID, taskID, firstJob := e.createProjectTask(t, "text", "Write a short brief about local-first software.")
	second, err := e.jobs.Create(ctx, taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	sink := strategy.NewRepo(e.db)
	e.eng.SetStrategySink(sink)
	if _, err := sink.CreateProfile(ctx, strategy.Profile{
		JobID: second.ID, ProjectID: projectID, Archetype: "text",
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.eng.state.SetReflectCount(ctx, second.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: second.ID, Data: map[string]any{
			"event": "divergence_start", "candidates": 2,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.DB().ExecContext(ctx,
		`UPDATE jobs SET state = 'completed' WHERE id IN (?, ?)`, firstJob, second.ID); err != nil {
		t.Fatal(err)
	}

	e.eng.FinalizeTerminal(ctx, second.ID)
	out, err := sink.GetOutcomeByJob(ctx, second.ID)
	if err != nil {
		t.Fatalf("outcome: %v", err)
	}
	if out.ReflectionLoops != 2 {
		t.Errorf("reflection loops = %d, want 2 (outcome written before scratch clear)", out.ReflectionLoops)
	}
	if out.Candidates != 2 {
		t.Errorf("candidates = %d, want the actual first-cycle arm 2", out.Candidates)
	}
	if out.Retries != 1 {
		t.Errorf("retries = %d, want 1 (second job for the same task)", out.Retries)
	}
}
