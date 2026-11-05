package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/job"
)

// fakeOperatorGate is a manually-controlled Deep/Full/Lite/Stopped gate.
type fakeOperatorGate struct {
	mu      sync.Mutex
	allowed bool
}

func (f *fakeOperatorGate) Allowed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.allowed
}

func (f *fakeOperatorGate) set(v bool) {
	f.mu.Lock()
	f.allowed = v
	f.mu.Unlock()
}

func waitForState(t *testing.T, e *testEnv, id string, want job.State, within time.Duration) job.Job {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		j, err := e.jobs.Get(context.Background(), id)
		if err == nil && j.State == want {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, _ := e.jobs.Get(context.Background(), id)
	t.Fatalf("job state = %s, want %s", j.State, want)
	return job.Job{}
}

// TestOperatorGateHoldsQueuedWork proves a stopped Athanor starts nothing:
// a direct Run stays queued and makes no model calls, like the kill switch.
func TestOperatorGateHoldsQueuedWork(t *testing.T) {
	e := newEnv(t)
	gate := &fakeOperatorGate{}
	e.eng.SetOperatorGate(gate)

	id := e.submit(t)
	e.eng.Run(context.Background(), id)
	j, err := e.jobs.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateQueued {
		t.Fatalf("state = %s, want queued while stopped", j.State)
	}
	if e.ollama.calls != 0 {
		t.Errorf("llm calls = %d, want 0 while stopped", e.ollama.calls)
	}
}

// TestOperatorGateRejectsEnqueue proves Enqueue leaves work queued rather than
// reserving a slot or an admission horizon grant while stopped.
func TestOperatorGateRejectsEnqueue(t *testing.T) {
	e := newEnv(t)
	gate := &fakeOperatorGate{}
	e.eng.SetOperatorGate(gate)

	id := e.submit(t)
	e.eng.Enqueue(id)
	time.Sleep(150 * time.Millisecond)
	j, err := e.jobs.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateQueued {
		t.Fatalf("state = %s, want queued while stopped", j.State)
	}
	if e.ollama.calls != 0 {
		t.Errorf("llm calls = %d, want 0 while stopped", e.ollama.calls)
	}
	if got := atomic.LoadInt64(&e.eng.inFlight); got != 0 {
		t.Errorf("in-flight = %d, want 0 while stopped", got)
	}
}

// TestOperatorPauseAllAndResumeAll proves the red-button path: active work
// parks in `paused` (never `cancelled`/`failed`), its pod is released, and
// ResumeAll re-drives it to completion.
func TestOperatorPauseAllAndResumeAll(t *testing.T) {
	e := newEnv(t)
	gate := &fakeOperatorGate{allowed: true}
	e.eng.SetOperatorGate(gate)
	e.ollama.WithDelay(500 * time.Millisecond)

	id := e.submit(t)
	e.eng.Enqueue(id)

	// Wait until the job is actually running a phase.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := e.jobs.Get(context.Background(), id)
		if err == nil && j.State != job.StateQueued && j.State != job.StateContextBuilding {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	gate.set(false)
	e.eng.PauseAll(context.Background())
	paused := waitForState(t, e, id, job.StatePaused, 10*time.Second)
	if paused.PausedFrom == "" {
		t.Fatal("paused job has no paused_from; ResumeAll cannot re-drive it")
	}

	// Stopped work must not resume on its own.
	time.Sleep(200 * time.Millisecond)
	if j, _ := e.jobs.Get(context.Background(), id); j.State != job.StatePaused {
		t.Fatalf("stopped job left paused = %s", j.State)
	}

	gate.set(true)
	e.eng.ResumeAll(context.Background())
	waitForState(t, e, id, job.StateCompleted, 30*time.Second)
}

// TestOperatorResumeAllRequeuesHeldWork proves queued work starts after an
// explicit resume, not before.
func TestOperatorResumeAllRequeuesHeldWork(t *testing.T) {
	e := newEnv(t)
	gate := &fakeOperatorGate{}
	e.eng.SetOperatorGate(gate)

	id := e.submit(t)
	e.eng.Run(context.Background(), id)
	waitForState(t, e, id, job.StateQueued, time.Second)

	gate.set(true)
	e.eng.ResumeAll(context.Background())
	waitForState(t, e, id, job.StateCompleted, 30*time.Second)
}
