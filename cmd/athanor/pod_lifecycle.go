package main

import (
	"context"
	"errors"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/jobpod"
)

// podManager is the slice of jobpod.Manager the lifecycle adapter needs.
type podManager interface {
	Start(ctx context.Context, spec jobpod.Spec) (*jobpod.Pod, error)
	Stop(ctx context.Context, id string) error
	Get(id string) (*jobpod.Pod, error)
}

// podLifecycleAdapter is the M2-T4b.5 engine.PodLifecycle over
// jobpod.Manager (ADR-0024 §2). The pod is a long-lived idle container
// (`sleep infinity`) that tool calls are exec'd into: the engine starts
// it before the `code` archetype's pod sub-steps and stops it at
// terminal state. It is the mirror of podExecutorAdapter — one starts,
// the other execs; both ride the same manager.
type podLifecycleAdapter struct {
	pods podManager
	cfg  *config.Config
}

var _ engine.PodLifecycle = (*podLifecycleAdapter)(nil)

func newPodLifecycle(pods podManager, cfg *config.Config) *podLifecycleAdapter {
	return &podLifecycleAdapter{pods: pods, cfg: cfg}
}

// Ensure starts the job's pod unless one already exists. An empty
// job_pod.image returns an error rather than starting a pod with no
// image; the engine audits and logs it (the tool route then answers 503).
func (a *podLifecycleAdapter) Ensure(ctx context.Context, jobID string) error {
	if _, err := a.pods.Get(jobID); err == nil {
		return nil
	} else if !errors.Is(err, jobpod.ErrNotFound) {
		return err
	}
	if a.cfg.JobPod.Image == "" {
		return errors.New("job_pod.image is empty; cannot start a Job Pod")
	}
	_, err := a.pods.Start(ctx, jobpod.Spec{
		ID:      jobID,
		Image:   a.cfg.JobPod.Image,
		Command: []string{"sleep", "infinity"},
		ResourceLimits: jobpod.Limits{
			PidsLimit: a.cfg.JobPod.PidsLimit,
			MemoryMB:  a.cfg.JobPod.MemoryMB,
			CPUs:      a.cfg.JobPod.CPUs,
		},
	})
	if err != nil && !errors.Is(err, jobpod.ErrAlreadyExists) {
		return err
	}
	return nil
}

// Stop tears the pod down. Idempotent: an unknown job is a no-op.
func (a *podLifecycleAdapter) Stop(ctx context.Context, jobID string) error {
	err := a.pods.Stop(ctx, jobID)
	if err != nil && !errors.Is(err, jobpod.ErrNotFound) {
		return err
	}
	return nil
}
