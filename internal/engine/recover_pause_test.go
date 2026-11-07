package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
)

type recordingPodLifecycle struct {
	stopped []string
}

func (r *recordingPodLifecycle) Ensure(context.Context, string, string) error { return nil }
func (r *recordingPodLifecycle) Stop(_ context.Context, jobID string) error {
	r.stopped = append(r.stopped, jobID)
	return nil
}

func TestRecoverResumesTransientPauseOnly(t *testing.T) {
	e := newEnv(t)
	_, _, jobID := e.createProjectTask(t, "text", "Write a brief about local-first software.")
	ctx := context.Background()
	for _, to := range []job.State{job.StateContextBuilding, job.StatePlanning, job.StatePaused} {
		if _, err := e.jobs.Transition(ctx, jobID, to); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	e.eng.audit(ctx, jobID, map[string]any{"event": "job_paused", "reason": "power"})
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if !e.eng.resumableOnBoot(ctx, j) {
		t.Fatal("power pause should be resumable when the gate is open")
	}

	e.eng.audit(ctx, jobID, map[string]any{"event": "job_paused", "reason": "operator"})
	if e.eng.resumableOnBoot(ctx, j) {
		t.Fatal("manual pause must not resume automatically")
	}
}

func TestReapIdlePodsStopsParkedJobs(t *testing.T) {
	e := newEnv(t)
	_, _, pausedID := e.createProjectTask(t, "text", "Write a brief about local-first software.")
	_, _, awaitingID := e.createProjectTask(t, "text", "Write another brief about local-first software.")
	ctx := context.Background()
	if _, err := e.jobs.Transition(ctx, pausedID, job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}
	if _, err := e.jobs.Transition(ctx, pausedID, job.StatePaused); err != nil {
		t.Fatal(err)
	}
	if _, err := e.jobs.Transition(ctx, awaitingID, job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}
	if _, err := e.jobs.Transition(ctx, awaitingID, job.StateAwaitingApproval); err != nil {
		t.Fatal(err)
	}
	rec := &recordingPodLifecycle{}
	e.eng.podLifecycle = rec
	e.eng.ReapIdlePods(ctx)
	if len(rec.stopped) != 2 {
		t.Fatalf("stopped = %v, want paused and awaiting jobs", rec.stopped)
	}
}
