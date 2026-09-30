package jobpod

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// exitError is a fake *exec.ExitError: it satisfies the exitCoder
// interface the manager reads to recover a command's non-zero exit code
// without internal/jobpod importing os/exec (Gate G1 rule 1).
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e exitError) ExitCode() int { return e.code }

// startedManager returns a manager with one just-started pod and the
// fake client that recorded the Start invocation.
func startedManager(t *testing.T) (Manager, *fakeClient) {
	t.Helper()
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return m, client
}

// TestExec_ArgvWithStdin pins the exec argv shape (ADR-0024 §2/§3):
// `-i` is present only when stdin is supplied, and it appears before the
// container id.
func TestExec_ArgvWithStdin(t *testing.T) {
	m, client := startedManager(t)
	res, err := m.Exec(context.Background(), goodID, ExecSpec{
		Command: []string{"python", "-"},
		Stdin:   []byte("print(1)"),
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	calls := client.Calls()
	last := calls[len(calls)-1]
	want := []string{"exec", "-i", goodID, "python", "-"}
	if !reflect.DeepEqual(last, want) {
		t.Errorf("exec argv = %v, want %v", last, want)
	}
	stdins := client.Stdins()
	if got := string(stdins[len(stdins)-1]); got != "print(1)" {
		t.Errorf("stdin = %q, want %q", got, "print(1)")
	}
}

// TestExec_NoStdinOmitsInteractiveFlag asserts a command with no stdin
// does not carry `-i`.
func TestExec_NoStdinOmitsInteractiveFlag(t *testing.T) {
	m, client := startedManager(t)
	if _, err := m.Exec(context.Background(), goodID, ExecSpec{Command: []string{"pytest", "-q"}}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	calls := client.Calls()
	last := calls[len(calls)-1]
	want := []string{"exec", goodID, "pytest", "-q"}
	if !reflect.DeepEqual(last, want) {
		t.Errorf("exec argv = %v, want %v", last, want)
	}
}

// TestExec_NonZeroExitIsAResult is the load-bearing distinction: a
// command that runs and fails is a normal ExecResult, not an error.
func TestExec_NonZeroExitIsAResult(t *testing.T) {
	client := &fakeClient{responder: func(args []string) ([]byte, []byte, error) {
		if len(args) > 0 && args[0] == "run" {
			return nil, nil, nil // Start must succeed
		}
		return []byte("boom"), []byte("trace"), exitError{code: 3}
	}}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := m.Exec(context.Background(), goodID, ExecSpec{Command: []string{"pytest", "-q"}})
	if err != nil {
		t.Fatalf("Exec returned error for a non-zero exit: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if res.Stdout != "boom" || res.Stderr != "trace" {
		t.Errorf("stdout/stderr = %q/%q, want boom/trace", res.Stdout, res.Stderr)
	}
}

// TestExec_PodmanFailureIsAnError asserts a podman-level failure (not an
// exit-coded command failure) surfaces as an error.
func TestExec_PodmanFailureIsAnError(t *testing.T) {
	client := &fakeClient{responder: func(args []string) ([]byte, []byte, error) {
		if len(args) > 0 && args[0] == "run" {
			return nil, nil, nil // Start must succeed
		}
		return nil, []byte("no such container"), errors.New("boom")
	}}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := m.Exec(context.Background(), goodID, ExecSpec{Command: []string{"pytest", "-q"}}); err == nil {
		t.Fatal("Exec: want error for a podman-level failure, got nil")
	}
}

func TestExec_UnknownPod(t *testing.T) {
	m := New(&fakeClient{}, &stubFreezer{}, "")
	_, err := m.Exec(context.Background(), goodID, ExecSpec{Command: []string{"pytest", "-q"}})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestExec_RequiresCommand(t *testing.T) {
	m, _ := startedManager(t)
	_, err := m.Exec(context.Background(), goodID, ExecSpec{})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("err = %v, want ErrInvalidSpec", err)
	}
}

// TestExec_NotRunningPod places a stopped entry in the map directly (the
// supervisor would normally have removed it) to pin the ErrNotRunning
// branch.
func TestExec_NotRunningPod(t *testing.T) {
	m := New(&fakeClient{}, &stubFreezer{}, "")
	concrete, ok := m.(*manager)
	if !ok {
		t.Fatalf("manager type = %T, want *manager", m)
	}
	concrete.mu.Lock()
	concrete.pods[goodID] = &podEntry{pod: &Pod{ID: goodID, State: StateStopped}}
	concrete.mu.Unlock()

	_, err := m.Exec(context.Background(), goodID, ExecSpec{Command: []string{"pytest", "-q"}})
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v, want ErrNotRunning", err)
	}
}
