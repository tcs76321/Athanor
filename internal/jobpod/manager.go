package jobpod

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// supervisionInterval is the cadence of the per-pod `podman inspect`
// poll. Two seconds matches internal/job.Recover's active-job scan
// cadence and is short enough that a crashed pod is noticed promptly
// without flooding the host with inspect calls.
const supervisionInterval = 2 * time.Second

// podmanInspectFormat is the `podman inspect` template the supervisor
// uses to read the container's state. Two fields, space-separated.
const podmanInspectFormat = "{{.State.Status}} {{.State.ExitCode}}"

// manager is the production Manager impl. Concurrency-safe.
type manager struct {
	client    Client
	freezer   Freezer
	tokenBase string // base dir for per-job token dirs; <state-dir>/tokens
	mu        sync.RWMutex
	pods      map[string]*podEntry
}

type podEntry struct {
	pod      *Pod
	stopC    chan struct{} // closed by the supervisor when the pod terminates
	tokenDir string        // host dir bind-mounted to /run/athanor; "" if no token
	token    string        // the per-job secret; never logged
}

// New returns a Manager ready for use. The supervisor goroutines
// start lazily on the first Start call and stop when the pod's
// state transitions to a terminal state (M2-T5 expands the test).
//
// tokenBase is the host directory under which per-job token dirs are
// created (typically <state-dir>/tokens). The base is created with
// 0700 perms at construction. An empty string disables token-dir
// generation; tests that do not exercise tokens may pass "".
func New(client Client, freezer Freezer, tokenBase string) Manager {
	if tokenBase != "" {
		// Best-effort: if MkdirAll fails here, the first Start
		// that needs a token dir will surface the error. We do
		// not return an error from New to keep the existing
		// call sites (which don't expect a constructor error)
		// unchanged.
		_ = os.MkdirAll(tokenBase, 0o700)
	}
	return &manager{
		client:    client,
		freezer:   freezer,
		tokenBase: tokenBase,
		pods:      map[string]*podEntry{},
	}
}

// validate enforces the documented Spec shape. Tight on the ID
// (UUID v4) so a future caller cannot smuggle in a non-UUID that
// podman would silently accept.
var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (m *manager) validateSpec(spec Spec) error {
	if !uuidV4Pattern.MatchString(spec.ID) {
		return fmt.Errorf("%w: ID must be a v4 UUID, got %q", ErrInvalidSpec, spec.ID)
	}
	if spec.Image == "" {
		return fmt.Errorf("%w: image is required", ErrInvalidSpec)
	}
	if len(spec.Command) == 0 {
		return fmt.Errorf("%w: command is required", ErrInvalidSpec)
	}
	return nil
}

// Start brings up a new pod. Steps:
//  1. validate the spec (no client call yet)
//  2. check the freezer (§22.1)
//  3. check for duplicate ID
//  4. generate a token dir if the spec doesn't supply one (M2-T3)
//  5. build the argv
//  6. invoke the client (podman run --detach)
//  7. register the pod in the in-memory map (with tokenDir for
//     teardown)
//  8. start the supervisor goroutine
//
// On any failure between step 4 and step 7 the token dir is removed
// so it doesn't leak to disk.
func (m *manager) Start(ctx context.Context, spec Spec) (*Pod, error) {
	if err := m.validateSpec(spec); err != nil {
		return nil, err
	}
	if m.freezer != nil && m.freezer.Frozen() {
		return nil, ErrFrozen
	}

	m.mu.Lock()
	if _, exists := m.pods[spec.ID]; exists {
		m.mu.Unlock()
		return nil, ErrAlreadyExists
	}
	m.mu.Unlock()

	// Step 4: token dir. If the spec supplies both Token and
	// TokenDir we trust the caller (engine passes a pre-issued
	// pair in the M2-T4 path). If only one is set we reject —
	// the two must travel together or not at all. If neither is
	// set and the manager has a tokenBase, we generate a fresh
	// dir + token.
	tokenDir := spec.TokenDir
	switch {
	case spec.Token == "" && spec.TokenDir == "":
		if m.tokenBase != "" {
			dir, tok, err := NewTokenDir(m.tokenBase, spec.ID)
			if err != nil {
				return nil, fmt.Errorf("generating token dir: %w", err)
			}
			tokenDir = dir
			spec.Token = tok
			spec.TokenDir = dir
		}
	case spec.Token != "" && spec.TokenDir != "":
		// Caller-supplied: trust it. (M2-T4 will pass these
		// through from a separate TokenIssuer.)
	default:
		return nil, fmt.Errorf("%w: Token and TokenDir must both be set or both empty", ErrInvalidSpec)
	}

	args := buildArgs(spec)
	if _, stderr, err := m.client.Run(ctx, args...); err != nil {
		// Pod didn't come up; remove any token dir we created.
		_ = RemoveTokenDir(tokenDir)
		// Surface podman's stderr: the raw exit code (often 125) is
		// useless for diagnosis without it.
		return nil, fmt.Errorf("podman run: %w: %s", err, strings.TrimSpace(string(stderr)))
	}

	pod := &Pod{ID: spec.ID, State: StatePending}
	entry := &podEntry{
		pod:      pod,
		stopC:    make(chan struct{}),
		tokenDir: tokenDir,
		token:    spec.Token,
	}

	m.mu.Lock()
	m.pods[spec.ID] = entry
	m.mu.Unlock()

	go m.supervise(ctx, spec.ID, entry)
	return pod, nil
}

// Exec runs one command inside a live pod (ADR-0024 §2).
//
// The pod is looked up first: an unknown job is ErrNotFound, and a pod
// that is neither pending nor running is ErrNotRunning. The argv is
// built by buildExecArgs (Gate G2's exec-argv arm guards it). A non-zero
// command exit is a normal result, not an error — the error return is
// reserved for a podman-level failure (missing binary, canceled
// context), so the engine can tell "the code failed" from "the pod
// broke".
func (m *manager) Exec(ctx context.Context, jobID string, spec ExecSpec) (ExecResult, error) {
	if len(spec.Command) == 0 {
		return ExecResult{}, fmt.Errorf("%w: command is required", ErrInvalidSpec)
	}
	m.mu.RLock()
	entry, ok := m.pods[jobID]
	m.mu.RUnlock()
	if !ok {
		return ExecResult{}, ErrNotFound
	}
	if entry.pod.State != StatePending && entry.pod.State != StateRunning {
		return ExecResult{}, fmt.Errorf("%w: pod %s is %s", ErrNotRunning, jobID, entry.pod.State)
	}

	args := buildExecArgs(jobID, len(spec.Stdin) > 0, spec.Command)
	start := time.Now()
	stdout, stderr, err := m.client.RunStdin(ctx, spec.Stdin, args...)
	res := ExecResult{
		Stdout:     string(stdout),
		Stderr:     string(stderr),
		DurationMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		var ec exitCoder
		if errors.As(err, &ec) {
			// The command ran and returned non-zero. Normal result.
			res.ExitCode = ec.ExitCode()
			return res, nil
		}
		return ExecResult{}, fmt.Errorf("podman exec: %w", err)
	}
	return res, nil
}

// exitCoder is satisfied by *exec.ExitError. It is declared structurally
// so internal/jobpod never imports os/exec (Gate G1 rule 1): execClient
// in cmd/athanor wraps the real *exec.ExitError with %w, so errors.As
// reaches it without an import.
type exitCoder interface{ ExitCode() int }

// supervise polls `podman inspect` until the container reports
// `exited` or `stopped`, then updates the in-memory Pod. Polling
// is the simplest correct primitive; events are M2-T5 territory.
// On a terminal-state observation the per-job token dir is removed
// — the container is gone and the secret should not outlive it.
func (m *manager) supervise(ctx context.Context, id string, entry *podEntry) {
	defer close(entry.stopC)
	// defer the token-dir removal so it runs whether we exit via
	// terminal state, ctx cancellation, or a panic. Stop is a
	// separate path and removes the dir itself.
	defer func() {
		// Re-read entry.tokenDir under the lock: a concurrent
		// Stop may have already cleared it. Either way, removing
		// a missing dir is a no-op.
		m.mu.RLock()
		td := entry.tokenDir
		m.mu.RUnlock()
		_ = RemoveTokenDir(td)
	}()
	ticker := time.NewTicker(supervisionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		stdout, stderr, err := m.client.Run(ctx, "inspect", "--format", podmanInspectFormat, id)
		if err != nil {
			// Inspect may fail transiently (e.g. container is being
			// created). Treat as "still running" and try again.
			slog.Debug("jobpod: inspect failed, retrying", "pod", id, "stderr", string(stderr), "err", err)
			continue
		}
		status, exitCode := parseInspect(string(stdout))
		switch status {
		case "running":
			m.setState(id, StateRunning, nil)
		case "exited", "stopped":
			if exitCode == 0 {
				m.setState(id, StateStopped, nil)
			} else {
				m.setState(id, StateFailed, fmt.Errorf("podman exit code %d", exitCode))
			}
			// M2-T5: drop the entry from the in-memory map on
			// terminal state. The entry is no longer live; the
			// token-dir removal above covers secret cleanup. Get
			// and TokenFor both return ErrNotFound from here on,
			// which matches the Stop path and is what the engine
			// and HTTP surface already handle (TokenFor's
			// "no active pod" error is the documented behavior
			// for a job whose pod has terminated).
			m.forget(id)
			return
		default:
			// Unknown status (paused, dead, etc.). Log and keep
			// polling; the next tick will resolve.
			slog.Debug("jobpod: unexpected pod status", "pod", id, "status", status, "exit", exitCode)
		}
	}
}

// setState updates the in-memory Pod under the write lock.
func (m *manager) setState(id string, state State, exitErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.pods[id]
	if !ok {
		return
	}
	entry.pod.State = state
	entry.pod.ExitErr = exitErr
}

// forget removes the entry from the in-memory map. Called by the
// supervisor when it observes a terminal state (exited/stopped).
// Idempotent: removing an unknown id is a no-op. The token dir is
// removed by the supervisor's deferred block, not here, so the
// cleanup ordering is the same regardless of which path tore the
// pod down.
func (m *manager) forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pods, id)
}

// Stop force-removes a pod. Idempotent: stopping a stopped pod is
// a no-op. Returns ErrNotFound for an unknown ID. Stops do not
// consult the freezer — teardown must always be possible. On a
// successful (or failed-but-proceeding) Stop the per-job token dir
// is removed so secrets don't outlive the pod.
func (m *manager) Stop(ctx context.Context, id string) error {
	m.mu.RLock()
	entry, exists := m.pods[id]
	m.mu.RUnlock()
	if !exists {
		return ErrNotFound
	}
	if _, _, err := m.client.Run(ctx, "rm", "-f", id); err != nil {
		// A failed stop is not necessarily fatal: the pod may have
		// already exited. We remove it from the in-memory map
		// regardless so future Stop calls are no-ops.
		slog.Warn("jobpod: stop failed; removing from in-memory map", "pod", id, "err", err)
	}
	// Remove the token dir regardless of whether podman rm
	// succeeded. The container is gone from our map; the
	// directory's job is done.
	_ = RemoveTokenDir(entry.tokenDir)
	m.mu.Lock()
	delete(m.pods, id)
	m.mu.Unlock()
	return nil
}

// Get returns the in-memory view of a pod. No I/O.
func (m *manager) Get(id string) (*Pod, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.pods[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &Pod{ID: entry.pod.ID, State: entry.pod.State, ExitErr: entry.pod.ExitErr}, nil
}

// TokenFor returns the active token for a job ID, or
// ErrTokenNotFound if no active pod exists. The returned string is
// the secret itself; the internal API auth middleware is the only
// caller, and it must not log the value. Returns an empty string
// when the entry exists but has no token (e.g. a pod started with
// Token==""), which the middleware treats as an auth failure.
func (m *manager) TokenFor(jobID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.pods[jobID]
	if !ok {
		return "", ErrNotFound
	}
	return entry.token, nil
}

// parseInspect splits `podman inspect` output into (status, exitCode).
// The documented two-token format is "<status> <exitCode>"; we fall
// back to a whole-string status when there is no exit code.
func parseInspect(s string) (string, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", 0
	}
	if len(fields) == 1 {
		return fields[0], 0
	}
	exitCode := 0
	if _, err := fmt.Sscanf(fields[len(fields)-1], "%d", &exitCode); err != nil {
		return strings.Join(fields, " "), 0
	}
	return strings.Join(fields[:len(fields)-1], " "), exitCode
}
