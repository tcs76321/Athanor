package engine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

// fakeLifecycle is an engine.PodLifecycle that records Ensure/Stop and the
// runner call count observed at Ensure time (to prove ensure-before-exec
// ordering).
type fakeLifecycle struct {
	mu      sync.Mutex
	runner  *fakeRunner
	ensured []string
	stopped []string
	atCount []int
	err     error
}

func (f *fakeLifecycle) Ensure(_ context.Context, jobID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, jobID)
	if f.runner != nil {
		f.atCount = append(f.atCount, f.runner.CallCount())
	}
	return f.err
}

func (f *fakeLifecycle) Stop(_ context.Context, jobID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, jobID)
	return nil
}

func (f *fakeLifecycle) snapshot() (ensured, stopped []string, atCount []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ensured...), append([]string(nil), f.stopped...), append([]int(nil), f.atCount...)
}

// TestStopPods_StopsActiveJobPods proves the F5 (ADR-0060) shutdown teardown:
// every active (non-terminal) job's pod is stopped, and a nil seam is a no-op.
func TestStopPods_StopsActiveJobPods(t *testing.T) {
	env := newEnv(t)
	lc := &fakeLifecycle{}
	env.eng.podLifecycle = lc

	jobID := env.submitCode(t) // queued → non-terminal
	env.eng.StopPods(context.Background())

	_, stopped, _ := lc.snapshot()
	if len(stopped) != 1 || stopped[0] != jobID {
		t.Fatalf("stopped = %v, want [%s]", stopped, jobID)
	}

	// A nil seam must not panic.
	env.eng.podLifecycle = nil
	env.eng.StopPods(context.Background())
}

// TestPodLifecycle_EnsureBeforeExecThenStopOnTerminal pins the ADR-0024 §2
// lifecycle: the pod is ensured before the first pod sub-step (so the
// runner's TokenFor exists) and stopped once the job is terminal. It also
// proves the seam does not change exec behavior (still 6 runner calls).
func TestPodLifecycle_EnsureBeforeExecThenStopOnTerminal(t *testing.T) {
	env := newEnv(t)
	lc := &fakeLifecycle{runner: env.runner}
	env.eng.podLifecycle = lc

	jobID := env.submitCode(t)
	env.eng.Run(context.Background(), jobID)

	ensured, stopped, atCount := lc.snapshot()
	if len(ensured) == 0 {
		t.Fatal("Ensure was never called for a code-archetype job")
	}
	if atCount[0] != 0 {
		t.Errorf("Ensure ran after %d runner calls, want 0 (ensure-before-exec)", atCount[0])
	}
	if len(stopped) != 1 || stopped[0] != jobID {
		t.Errorf("stopped = %v, want [%s]", stopped, jobID)
	}
	if got := env.runner.CallCount(); got != 8 {
		t.Errorf("runner calls = %d, want 8 (6 per-candidate + 2 final F4-T3 verification)", got)
	}
}

// TestPodLifecycle_NotCalledForTextArchetype: ensurePod is only reached in
// the code-archetype branch, so a text job never starts a pod.
func TestPodLifecycle_NotCalledForTextArchetype(t *testing.T) {
	env := newEnv(t)
	lc := &fakeLifecycle{runner: env.runner}
	env.eng.podLifecycle = lc

	jobID := env.submit(t)
	env.eng.Run(context.Background(), jobID)

	ensured, _, _ := lc.snapshot()
	if len(ensured) != 0 {
		t.Errorf("Ensure calls = %d for a text job, want 0", len(ensured))
	}
}

// TestPodLifecycle_EnsureErrorSoftFailsAndAudits: a failed Ensure is
// audited (`pod_start_failed`) and does not block the pod sub-steps, which
// surface their own error if the pod is genuinely required.
func TestPodLifecycle_EnsureErrorSoftFailsAndAudits(t *testing.T) {
	env := newEnv(t)
	lc := &fakeLifecycle{runner: env.runner, err: errors.New("job_pod.image is empty")}
	env.eng.podLifecycle = lc

	jobID := env.submitCode(t)
	env.eng.Run(context.Background(), jobID)

	if got := env.runner.CallCount(); got != 8 {
		t.Errorf("runner calls = %d, want 8 (an Ensure failure must not block the sub-steps)", got)
	}
	events, err := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		var d struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal([]byte(e.DataJSON), &d)
		if d.Event == "pod_start_failed" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a pod_start_failed audit event; got %d events", len(events))
	}
}

// TestPodLifecycle_NilSeamIsNoOp is the regression guard: the default
// (nil) seam leaves the pre-T4b behavior intact.
func TestPodLifecycle_NilSeamIsNoOp(t *testing.T) {
	env := newEnv(t) // podLifecycle is nil
	jobID := env.submitCode(t)
	env.eng.Run(context.Background(), jobID)
	if got := env.runner.CallCount(); got != 8 {
		t.Errorf("runner calls = %d, want 8 with a nil lifecycle seam", got)
	}
}
