// Package jobpod owns the lifecycle of a Podman Job Pod: creation,
// supervision, and teardown. Every pod comes up with the §21.2
// hardening flags; the package's job is to make that guarantee
// structural rather than aspirational.
//
// M2-T2 ships this package. The M2-T3 token mount and the M2-T4
// internal API are layered on top; the engine is wired to it later.
//
// The package does not import os/exec. Production wiring lives in
// cmd/athanor/jobpod_client.go so internal/gate/gate_test.go stays
// clean.
package jobpod

import "context"

// Spec is the input to Start. The package validates every field and
// rejects anything that would let a pod escape the §21.2 containment
// guarantees.
type Spec struct {
	// ID is the job UUID; also the container name. Must match the
	// v4 UUID shape produced by internal/ids.New.
	ID string
	// Image is the resolved image reference (e.g. "alpine:3.20").
	Image string
	// Command is the entrypoint + args.
	Command []string
	// Token is the per-job secret (16-byte random hex). Mounted at
	// /run/athanor/token via TokenDir. M2-T3 owns validation; M2-T2
	// just mounts it.
	Token string
	// TokenDir is the host directory containing a file named "token"
	// whose contents are Token. The pod bind-mounts it read-only.
	TokenDir string
	// ResourceLimits is the pids/memory/cpus set applied to the pod.
	ResourceLimits Limits
	// Env is the pod's environment. Empty in M2-T2; the token never
	// appears in env (ADR-0007).
	Env []string
}

// Limits is the per-pod resource cap. The defaults applied by Start
// when a field is zero are: PidsLimit=64, MemoryMB=512, CPUs=1.0.
type Limits struct {
	PidsLimit int
	MemoryMB  int
	CPUs      float64
}

// ExecSpec is the input to Exec: one command to run inside a live pod
// (ADR-0024 §2/§3). The caller supplies a closed-set command; the pod
// adds no privileges, mounts, or network options.
type ExecSpec struct {
	// Command is the argv to run inside the container. Required.
	Command []string
	// Stdin, when non-empty, is wired to the command's standard input.
	// execute_code pipes source this way so it never appears in argv or
	// the host process table (ADR-0024 §3).
	Stdin []byte
}

// ExecResult is the outcome of one Exec. ExitCode is the command's exit
// status — a non-zero code is a normal result (the command ran and
// failed), not an error; the error return is reserved for a podman-level
// failure (missing binary, unknown pod, canceled context). DurationMS is
// wall-clock milliseconds around the exec call.
type ExecResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMS int64
}

// State is a pod's lifecycle state, as tracked by the Manager.
type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateFailed  State = "failed"
)

// Pod is the manager's view of a container. The container itself
// lives in podman; this struct is the in-memory mirror.
type Pod struct {
	ID      string
	State   State
	ExitErr error
}

// Freezer is the kill-switch surface the manager consults before
// starting new pods (§22.1: frozen means no new work). Satisfied by
// *control.KillSwitch.
type Freezer interface {
	Frozen() bool
}

// Client is the podman subprocess surface. The default production
// impl lives in cmd/athanor/jobpod_client.go (so internal/gate stays
// clean). Tests use a fake.
type Client interface {
	// Run executes `podman <args...>`, returning stdout, stderr, and
	// the exit error. Cancellation via ctx is honored; a canceled
	// call returns ctx.Err().
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, err error)
	// RunStdin is Run with standard input wired to stdin (nil = no
	// input). It exists for `podman exec -i`, where execute_code pipes
	// source to the interpreter so it never appears in argv (ADR-0024
	// §3). A nil stdin behaves exactly like Run.
	RunStdin(ctx context.Context, stdin []byte, args ...string) (stdout, stderr []byte, err error)
}

// SweepResult reports what Sweep did at startup. Counts are exposed
// for observability and for the M2-T5 kill-9 integration test.
type SweepResult struct {
	Inspected int
	Removed   int
	Kept      int
}

// Manager is the public surface. The production impl lives in
// manager.go; tests use a fake. All methods are safe for concurrent
// use.
type Manager interface {
	// Start brings up a new pod with the spec's flags and registers
	// it. Returns ErrInvalidSpec, ErrFrozen, or ErrAlreadyExists
	// before any client call in the error cases.
	Start(ctx context.Context, spec Spec) (*Pod, error)
	// Exec runs a command inside a live pod and returns its result
	// (ADR-0024 §2). ExitCode is the command's status; a non-zero code
	// is a normal result, not an error. Returns ErrNotFound when no pod
	// exists for the job and ErrNotRunning when the pod cannot accept
	// an exec.
	Exec(ctx context.Context, jobID string, spec ExecSpec) (ExecResult, error)
	// Stop force-removes a pod. Idempotent: stopping a stopped pod
	// is a no-op. Returns ErrNotFound if no such pod.
	Stop(ctx context.Context, id string) error
	// Get returns the manager's current view of a pod. No I/O.
	Get(id string) (*Pod, error)
	// TokenFor returns the active token for a job ID, or
	// ErrTokenNotFound if no active pod exists for that ID. Used
	// by the internal API auth middleware to verify the bearer
	// presented by a Job Pod. The returned string is the secret
	// itself; callers must not log it.
	TokenFor(jobID string) (string, error)
	// Sweep force-removes any athanor-job-* container the manager
	// did not start itself. Called once at daemon boot (M2-T5
	// expands the test).
	Sweep(ctx context.Context) (SweepResult, error)
}
