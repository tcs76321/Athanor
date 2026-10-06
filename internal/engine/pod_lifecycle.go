package engine

import (
	"context"
	"log/slog"

	"github.com/tcs76321/athanor/internal/job"
)

// PodLifecycle is the engine's window onto the Job Pod lifecycle
// (ADR-0024 §2, M2-T4b.5). The engine ensures a pod exists before the
// `code` archetype's pod sub-steps — so the per-job token the runner
// presents (TokenFor) exists — and stops it once the job reaches a
// terminal state.
//
// A nil PodLifecycle is valid: the engine never starts a pod and the code
// sub-steps behave exactly as they did before M2-T4b (the runner's
// TokenFor then finds nothing, which was the pre-T4b state). Production
// wires the cmd/athanor adapter over jobpod.Manager.
type PodLifecycle interface {
	// Ensure makes a Job Pod exist for the job. Idempotent: a job whose
	// pod is already running is a no-op.
	Ensure(ctx context.Context, jobID string) error
	// Stop tears the job's Job Pod down. Idempotent: an unknown job is a
	// no-op.
	Stop(ctx context.Context, jobID string) error
}

// ensurePod brings up the job's Job Pod before a pod sub-step (ADR-0024
// §2). A nil seam is a no-op, so unit tests with a fake runner are
// unaffected. A failure is audited (category `podman`) and logged, not
// fatal: the sub-step that follows surfaces its own error if the pod is
// genuinely required.
func (e *Engine) ensurePod(ctx context.Context, j job.Job) {
	if e.podLifecycle == nil {
		return
	}
	if err := e.podLifecycle.Ensure(ctx, j.ID); err != nil {
		e.auditCat(ctx, j.ID, "podman", map[string]any{
			"event": "pod_start_failed", "error": err.Error(),
		})
		slog.Warn("engine: ensuring job pod", "job", j.ID, "err", err)
	}
}

// stopPod tears the job's Job Pod down at terminal state (ADR-0024 §2,
// §23.6). A nil seam is a no-op; a Stop error is logged, not fatal — the
// pod may already be gone, and the M2-T5 startup sweep is the backstop.
func (e *Engine) stopPod(ctx context.Context, jobID string) {
	if e.podLifecycle == nil {
		return
	}
	if err := e.podLifecycle.Stop(ctx, jobID); err != nil {
		slog.Warn("engine: stopping job pod", "job", jobID, "err", err)
	}
}

// StopPods tears down the Job Pod of every active (non-terminal) job during
// graceful shutdown, so pods do not linger until the next boot's M2-T5 sweep
// (F5, ADR-0060).
//
// It deliberately does not wait for in-flight phases: the daemon's shutdown
// posture is abandon-and-recover. State is committed after every transition
// (§23.3), and Recover resumes a non-terminal job on the next start (§23.6).
// A nil seam or a listing error is a logged no-op — the startup sweep is the
// backstop.
func (e *Engine) StopPods(ctx context.Context) {
	if e.podLifecycle == nil || e.jobs == nil {
		return
	}
	active, err := e.jobs.Active(ctx)
	if err != nil {
		slog.Warn("engine: listing active jobs for pod cleanup", "err", err)
		return
	}
	for _, j := range active {
		if j.State.Terminal() {
			continue
		}
		e.stopPod(ctx, j.ID)
	}
}
