package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/job"
)

type fakeLaneAdmission struct {
	acquired atomic.Int64
	released atomic.Int64
	lane     atomic.Value
}

func (f *fakeLaneAdmission) Acquire(ctx context.Context, lane string) (func(), error) {
	f.acquired.Add(1)
	f.lane.Store(lane)
	return func() { f.released.Add(1) }, nil
}

func (f *fakeLaneAdmission) MaxConcurrentJobs() int { return 1 }

func TestLaneAdmissionUsedByEnqueue(t *testing.T) {
	env := newEnv(t)
	fa := &fakeLaneAdmission{}
	env.eng.SetAdmission(fa)
	jobID := env.submit(t)
	env.eng.Enqueue(jobID)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := env.jobs.Get(context.Background(), jobID)
		if err == nil && j.State.Terminal() {
			if j.State != job.StateCompleted {
				t.Fatalf("state = %s, want completed", j.State)
			}
			if fa.acquired.Load() != 1 || fa.released.Load() != 1 {
				t.Errorf("acquire/release = %d/%d, want 1/1", fa.acquired.Load(), fa.released.Load())
			}
			if got, _ := fa.lane.Load().(string); got != "light" {
				t.Errorf("lane = %q, want light for a text job", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not complete")
}
