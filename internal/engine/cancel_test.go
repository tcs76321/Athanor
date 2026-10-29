package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/job"
)

// TestCancelStopsRunningJob proves a UI/system cancel reaches the phase
// context and commits `cancelled`, not `failed`.
func TestCancelStopsRunningJob(t *testing.T) {
	env := newEnv(t)
	env.ollama.WithDelay(2 * time.Second)
	jobID := env.submit(t)
	env.eng.Enqueue(jobID)

	// Wait until the goroutine has registered the cancel func and started.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, err := env.jobs.Get(context.Background(), jobID)
		if err == nil && j.State != job.StateQueued && j.State != job.StateContextBuilding {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !env.eng.Cancel(jobID) {
		t.Fatal("Cancel returned false; the job goroutine was not registered")
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := env.jobs.Get(context.Background(), jobID)
		if err == nil && j.State.Terminal() {
			if j.State != job.StateCancelled {
				t.Fatalf("state = %s, want cancelled", j.State)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not reach a terminal state after Cancel")
}
