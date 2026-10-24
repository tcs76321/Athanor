package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/project"
)

// repoSnapshotter stages a project repository for a Job Pod (ADR-0082).
// The production implementation is gitClient.Snapshot in git_client.go.
type repoSnapshotter interface {
	Snapshot(ctx context.Context, repoPath, dest string) error
}

// projectGetter is the narrow project lookup the lifecycle adapter needs.
type projectGetter interface {
	Get(ctx context.Context, id string) (project.Project, error)
}

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
	pods     podManager
	cfg      *config.Config
	projects projectGetter
	stateDir string
	snap     repoSnapshotter
}

var _ engine.PodLifecycle = (*podLifecycleAdapter)(nil)

func newPodLifecycle(pods podManager, cfg *config.Config, projects projectGetter, stateDir string, snap repoSnapshotter) *podLifecycleAdapter {
	return &podLifecycleAdapter{pods: pods, cfg: cfg, projects: projects, stateDir: stateDir, snap: snap}
}

// Ensure starts the job's pod unless one already exists. If the project has a
// repository, it stages a read-only snapshot under <state>/scratch/<job>/repo
// and mounts it at /workspace/repo (ADR-0082). A snapshot failure is logged
// and the pod starts without the mount: the job can still run against a
// self-contained candidate, and the reason is visible in the log.
func (a *podLifecycleAdapter) Ensure(ctx context.Context, jobID, projectID string) error {
	if _, err := a.pods.Get(jobID); err == nil {
		return nil
	} else if !errors.Is(err, jobpod.ErrNotFound) {
		return err
	}
	if a.cfg.JobPod.Image == "" {
		return errors.New("job_pod.image is empty; cannot start a Job Pod")
	}
	repoDir := a.stageRepo(ctx, jobID, projectID)
	_, err := a.pods.Start(ctx, jobpod.Spec{
		ID:      jobID,
		Image:   a.cfg.JobPod.Image,
		Command: []string{"sleep", "infinity"},
		RepoDir: repoDir,
		ResourceLimits: jobpod.Limits{
			PidsLimit: a.cfg.JobPod.PidsLimit,
			MemoryMB:  a.cfg.JobPod.MemoryMB,
			CPUs:      a.cfg.JobPod.CPUs,
			TmpfsMB:   a.cfg.JobPod.TmpfsMB,
		},
	})
	if err != nil && !errors.Is(err, jobpod.ErrAlreadyExists) {
		return err
	}
	return nil
}

// stageRepo creates the per-job snapshot. It returns "" when the project has
// no repository, the lookup fails, or the snapshot cannot be staged.
func (a *podLifecycleAdapter) stageRepo(ctx context.Context, jobID, projectID string) string {
	if a.projects == nil || a.snap == nil || a.stateDir == "" {
		return ""
	}
	p, err := a.projects.Get(ctx, projectID)
	if err != nil {
		slog.Warn("pod lifecycle: loading project for repository snapshot", "job", jobID, "err", err)
		return ""
	}
	if p.RepositoryPath == "" {
		return ""
	}
	dest := filepath.Join(a.stateDir, "scratch", jobID, "repo")
	if err := a.snap.Snapshot(ctx, p.RepositoryPath, dest); err != nil {
		slog.Warn("pod lifecycle: staging repository snapshot", "job", jobID, "repo", p.RepositoryPath, "err", err)
		_ = os.RemoveAll(dest)
		return ""
	}
	return dest
}

// Stop tears the pod down and removes the job's snapshot. Idempotent: an
// unknown job is a no-op. The scratch removal is best-effort; a boot sweep is
// the backstop.
func (a *podLifecycleAdapter) Stop(ctx context.Context, jobID string) error {
	err := a.pods.Stop(ctx, jobID)
	if err != nil && !errors.Is(err, jobpod.ErrNotFound) {
		return err
	}
	if a.stateDir != "" {
		_ = os.RemoveAll(filepath.Join(a.stateDir, "scratch", jobID))
	}
	return nil
}
