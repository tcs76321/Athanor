package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// podExecer is the slice of jobpod.Manager the adapter needs. Narrowing
// the dependency keeps the adapter testable with a fake and documents that
// it only ever execs — starting and stopping the pod is the engine
// lifecycle seam's job (M2-T4b.5).
type podExecer interface {
	Exec(ctx context.Context, jobID string, spec jobpod.ExecSpec) (jobpod.ExecResult, error)
}

// podExecutorAdapter is the M2-T4b internalapi.PodExecutor production
// implementation (ADR-0024 §2/§4). It runs each tool command inside the
// job's live Job Pod via jobpod.Manager.Exec and translates the manager's
// typed errors into the internalapi status contract. It is the same
// inversion shape as the gateway/context_swap adapters: internalapi holds
// the interface, cmd/ holds the concrete dependency.
type podExecutorAdapter struct {
	pods podExecer
	// image is cfg.JobPod.Image. Empty means the daemon cannot start a
	// pod, so every call refuses with ErrExecNotConfigured (→ 503) rather
	// than surfacing a confusing "no pod" 404 (ADR-0024 §6).
	image string
}

var _ internalapi.PodExecutor = (*podExecutorAdapter)(nil)

// newPodExecutor returns the production PodExecutor over the Job Pod
// manager.
func newPodExecutor(pods podExecer, image string) *podExecutorAdapter {
	return &podExecutorAdapter{pods: pods, image: image}
}

// RunCode pipes the source to `python -` on stdin so it never enters argv
// or the host process table (ADR-0024 §3). The handler validates Language
// against the closed set ("python"), so the interpreter is fixed here.
func (a *podExecutorAdapter) RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return a.exec(ctx, jobID, jobpod.ExecSpec{
		Command: []string{"python", "-"},
		Stdin:   []byte(req.Code),
	})
}

// RunTests runs the command through `sh -c` so a command line like
// `pytest -q` works without the adapter parsing it into argv. The base
// image must provide a POSIX shell (ADR-0024 §6); that is an operator
// precondition until M7-T7 ships a Job Pod image.
func (a *podExecutorAdapter) RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return a.exec(ctx, jobID, jobpod.ExecSpec{Command: []string{"sh", "-c", req.Command}})
}

// Lint is RunTests with the linter command (the default `ruff check .` is
// resolved server-side before the call).
func (a *podExecutorAdapter) Lint(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return a.exec(ctx, jobID, jobpod.ExecSpec{Command: []string{"sh", "-c", req.Command}})
}

// exec is the shared body. It refuses early when no image is configured,
// then translates jobpod's typed errors into the internalapi contract so
// the handler can map them to statuses without importing internal/jobpod.
func (a *podExecutorAdapter) exec(ctx context.Context, jobID string, spec jobpod.ExecSpec) (toolenvelope.ExecuteResult, error) {
	if a.image == "" {
		return toolenvelope.ExecuteResult{}, fmt.Errorf("%w: job_pod.image is not set", internalapi.ErrExecNotConfigured)
	}
	res, err := a.pods.Exec(ctx, jobID, spec)
	if err != nil {
		switch {
		case errors.Is(err, jobpod.ErrNotFound):
			return toolenvelope.ExecuteResult{}, internalapi.ErrNoPod
		case errors.Is(err, jobpod.ErrNotRunning):
			return toolenvelope.ExecuteResult{}, internalapi.ErrPodNotRunning
		default:
			return toolenvelope.ExecuteResult{}, fmt.Errorf("pod exec: %w", err)
		}
	}
	return toolenvelope.ExecuteResult{
		ExitCode:   res.ExitCode,
		Stdout:     res.Stdout,
		Stderr:     res.Stderr,
		DurationMS: res.DurationMS,
	}, nil
}
