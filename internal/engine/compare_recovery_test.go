package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
)

// TestPhaseCompareRecoversAfterAcceptance proves the crash window between
// artifact acceptance and the job's completed transition is idempotent. A
// rerun must not compare the accepted final against itself or supersede it.
func TestPhaseCompareRecoversAfterAcceptance(t *testing.T) {
	e := newEnv(t)
	projectID, taskID, jobID := e.createProjectTask(t, "text", "Write a short brief about local-first software.")
	ctx := context.Background()
	for _, to := range []job.State{
		job.StateContextBuilding, job.StatePlanning, job.StateDiverging,
		job.StateEvaluating, job.StateSynthesizing, job.StateComparing,
	} {
		if _, err := e.jobs.Transition(ctx, jobID, to); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	final, err := e.artifacts.CreateDraftFor(ctx, projectID, taskID, jobID,
		artifact.KindDocument, []byte("already accepted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.artifacts.SetStatus(ctx, final.ID, artifact.StatusCandidate); err != nil {
		t.Fatal(err)
	}
	if err := e.artifacts.SetStatus(ctx, final.ID, artifact.StatusAccepted); err != nil {
		t.Fatal(err)
	}

	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.eng.phaseCompare(ctx, j); err != nil {
		t.Fatalf("phaseCompare on an accepted final: %v", err)
	}
	got, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed", got.State)
	}
	stored, err := e.artifacts.Get(ctx, final.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != artifact.StatusAccepted {
		t.Fatalf("final status = %s, want accepted", stored.Status)
	}
}
