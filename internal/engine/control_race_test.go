package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/job"
)

// TestAbandonedWaitDoesNotUnderflowInFlight is the regression for the
// concurrency-cap leak: an Enqueue goroutine that returns before acquiring a
// slot must not release a slot it never took. Before the fix, a cancelled
// waiter decremented inFlight unconditionally, making the counter negative and
// silently allowing more concurrent jobs than the configured cap.
func TestAbandonedWaitDoesNotUnderflowInFlight(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env.eng.EnqueueContext(ctx, jobID)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&env.eng.inFlight) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt64(&env.eng.inFlight); got != 0 {
		t.Fatalf("inFlight = %d after an abandoned wait, want 0", got)
	}
}

// TestCancelQueuedJobCommitsCancelled proves a user cancel reaches a job that
// is still waiting for a slot: the job must commit `cancelled` (not stay
// queued forever) and must not touch the slot held by the running job.
func TestCancelQueuedJobCommitsCancelled(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)

	// Simulate a running job holding the single slot. No LLM call is needed:
	// the queued goroutine only inspects the counter.
	env.eng.cap = fixedCap{n: 1}
	atomic.StoreInt64(&env.eng.inFlight, 1)
	t.Cleanup(func() { atomic.StoreInt64(&env.eng.inFlight, 0) })

	env.eng.Enqueue(jobID)

	// Wait until the goroutine has registered its cancel func and is waiting.
	deadline := time.Now().Add(2 * time.Second)
	registered := false
	for time.Now().Before(deadline) {
		env.eng.cancelMu.Lock()
		_, registered = env.eng.cancels[jobID]
		env.eng.cancelMu.Unlock()
		if registered {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !registered {
		t.Fatal("queued job did not register a cancel func")
	}
	if !env.eng.Cancel(jobID) {
		t.Fatal("Cancel returned false for a queued job")
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, err := env.jobs.Get(context.Background(), jobID)
		if err == nil && j.State == job.StateCancelled {
			if got := atomic.LoadInt64(&env.eng.inFlight); got != 1 {
				t.Fatalf("inFlight = %d, want the running job's slot to stay reserved", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := env.jobs.Get(context.Background(), jobID)
	t.Fatalf("queued cancel state = %s, want cancelled", j.State)
}

// TestExternalPauseDuringPhaseDoesNotFailJob is the regression for the UI pause
// race: pausing a job while a phase is running used to make the phase's next
// transition fail, and the engine committed `failed`. A concurrent pause must
// be honored, not turned into a failure.
func TestExternalPauseDuringPhaseDoesNotFailJob(t *testing.T) {
	env := newEnv(t)
	// Slow each model call enough that a pause lands inside the planning
	// phase rather than between phases.
	env.ollama.WithDelay(700 * time.Millisecond)
	jobID := env.submit(t)
	env.eng.Enqueue(jobID)

	// Wait until the engine is inside the planning phase.
	deadline := time.Now().Add(5 * time.Second)
	var state job.State
	for time.Now().Before(deadline) {
		j, err := env.jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		state = j.State
		if state == job.StatePlanning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if state != job.StatePlanning {
		t.Fatalf("job never reached planning (last state %s)", state)
	}
	if _, err := env.jobs.Transition(context.Background(), jobID, job.StatePaused); err != nil {
		t.Fatalf("external pause: %v", err)
	}

	// Give the in-flight phase time to return and the engine time to re-read.
	time.Sleep(2 * time.Second)
	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StatePaused {
		t.Fatalf("state = %s, want paused (an external pause must not fail the job)", j.State)
	}
}
