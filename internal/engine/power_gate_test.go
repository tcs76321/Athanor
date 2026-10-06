package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/job"
)

// fakePauseGate is a manually-controlled §24 power gate.
type fakePauseGate struct {
	mu     sync.Mutex
	paused bool
}

func (f *fakePauseGate) Paused() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused
}

func (f *fakePauseGate) set(v bool) {
	f.mu.Lock()
	f.paused = v
	f.mu.Unlock()
}

// TestPowerGatePausesActiveJobAndResumes proves the M7-T1 pause path: an
// active job pauses at the phase boundary when the power gate closes, and
// ResumePaused re-drives it to completion when it reopens.
func TestPowerGatePausesActiveJobAndResumes(t *testing.T) {
	e := newEnv(t)
	gate := &fakePauseGate{}
	e.eng.SetPauseGate(gate)
	ctx := context.Background()

	// Drive a job to `planning` so it is active (a queued job would only be
	// held, mirroring freeze).
	id := e.submit(t)
	if _, err := e.jobs.Transition(ctx, id, job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}
	if _, err := e.jobs.Transition(ctx, id, job.StatePlanning); err != nil {
		t.Fatal(err)
	}

	gate.set(true)
	e.eng.Run(ctx, id)
	j, err := e.jobs.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StatePaused {
		t.Fatalf("state = %s, want paused (power gate closed)", j.State)
	}
	if j.PausedFrom != job.StatePlanning {
		t.Fatalf("paused_from = %s, want planning", j.PausedFrom)
	}

	gate.set(false)
	e.eng.ResumePaused(ctx)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final, err := e.jobs.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == job.StateCompleted {
			return
		}
		if final.State.Terminal() {
			t.Fatalf("resumed job reached %s, want completed", final.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("power-paused job never completed after ResumePaused")
}

// TestPowerGateHoldsQueuedJob proves the gate is consulted before any work
// starts: a queued job under a closed gate stays queued (it cannot transition
// to paused), exactly like the kill switch.
func TestPowerGateHoldsQueuedJob(t *testing.T) {
	e := newEnv(t)
	gate := &fakePauseGate{paused: true}
	e.eng.SetPauseGate(gate)

	id := e.submit(t)
	e.eng.Run(context.Background(), id)
	j, err := e.jobs.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateQueued {
		t.Fatalf("state = %s, want queued (held by power gate)", j.State)
	}
	if e.ollama.calls != 0 {
		t.Errorf("llm calls = %d, want 0 while the gate is closed", e.ollama.calls)
	}
}
