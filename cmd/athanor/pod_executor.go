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

// The Job Pod's root filesystem is read-only and its only writable location
// is the /tmp tmpfs (ADR-0007/0024). Materializing the candidate there is what
// lets a real test command see it. A dedicated, owner-scoped workspace mount
// is future work (F4-T0); this is the contained first step.
const (
	podScratch    = "/tmp"
	candidateFile = podScratch + "/solution.py"
)

// treeUnpacker is the fixed in-pod program that materializes a Files tree
// (ADR-0065). It reads the JSON manifest on stdin — content never touches
// argv (ADR-0024 §3) — and re-validates every path before writing. That
// re-validation is defense in depth behind toolenvelope.EncodeFiles: the pod
// is the last line of containment, so it refuses a traversal path even if the
// Core's check were bypassed. It contains no single quotes, so it is safe to
// wrap in single quotes for sh.
const treeUnpacker = `import sys,json,os
m=json.load(sys.stdin)
for f in m.get("files",[]):
    p=f["path"]
    if p=="" or p.startswith("/") or ".." in p.split("/") or "\x00" in p:
        sys.stderr.write("unsafe path: "+p+"\n"); sys.exit(2)
    d=os.path.dirname(p)
    if d: os.makedirs(d,exist_ok=True)
    open(p,"w").write(f["content"])
`

// RunCode materializes the candidate to a file in the pod's writable scratch
// and runs it. The source still travels on stdin (never argv or the host
// process table, ADR-0024 §3): `cat` receives it and writes the file, then the
// interpreter runs that file. Writing the file is what gives the subsequent
// RunTests call something on disk to test — previously `python -` left nothing
// behind and the real test command could only ever be a no-op. The handler
// validates Language against the closed set ("python"), so the interpreter and
// filename are fixed here.
func (a *podExecutorAdapter) RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	// ADR-0065: a multi-file candidate is staged as a tree rather than a
	// single program. Staging is the materialization step; the task's test
	// command (RunTests) is what runs against the tree.
	if len(req.Files) > 0 {
		manifest, err := toolenvelope.EncodeFiles(req.Files)
		if err != nil {
			return toolenvelope.ExecuteResult{}, fmt.Errorf("encoding candidate file tree: %w", err)
		}
		return a.exec(ctx, jobID, jobpod.ExecSpec{
			Command: []string{"sh", "-c", "mkdir -p " + podScratch + " && cd " + podScratch + " && python -c '" + treeUnpacker + "'"},
			Stdin:   manifest,
		})
	}
	return a.exec(ctx, jobID, jobpod.ExecSpec{
		Command: []string{"sh", "-c", "cat > " + candidateFile + " && python " + candidateFile},
		Stdin:   []byte(req.Code),
	})
}

// RunTests runs the command through `sh -c` in the scratch dir so a command
// line like `pytest -q` (or `python -c "import solution; ..."`) sees the file
// RunCode wrote. The base image must provide a POSIX shell (ADR-0024 §6); that
// is an operator precondition until M7-T7 ships a Job Pod image.
func (a *podExecutorAdapter) RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return a.exec(ctx, jobID, jobpod.ExecSpec{Command: []string{"sh", "-c", "cd " + podScratch + " && " + req.Command}})
}

// Lint is RunTests with the linter command (the default `ruff check .` is
// resolved server-side before the call).
func (a *podExecutorAdapter) Lint(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return a.exec(ctx, jobID, jobpod.ExecSpec{Command: []string{"sh", "-c", "cd " + podScratch + " && " + req.Command}})
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
