package jobpod

// M2-T4b end-to-end exec probe (ADR-0024; ROADMAP M2-T4b).
//
// This file runs a real hardened rootless Job Pod and drives
// `jobpod.Manager.Exec` against it: it starts a long-lived idle pod, pipes
// source to `python -` on stdin, and asserts the exit code / stdout come
// back through the same path the internal API's PodExecutor uses. It is
// the pod-in-the-loop complement to the unit tests in exec_test.go (which
// use the recording fake client).
//
// Gated by ATHANOR_RUN_INTEGRATION and a real podman with the probe image
// present. Developers run it with `make test-integration` (after
// `make integration-images`); CI runs it in the non-blocking `integration`
// job. Like security_test.go, this file uses os/exec; Gate G1 excludes
// _test.go files from the production-source walk.

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// execIntegrationImage is the Job Pod base image the exec probe uses. It
// must provide a POSIX shell, `sleep`, and `python` (ADR-0024 §6).
const execIntegrationImage = "python:3.12-alpine"

// runPodman shells out to the `podman` binary. It mirrors the production
// client in cmd/athanor/jobpod_client.go without importing package main.
func runPodman(ctx context.Context, stdin []byte, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "podman", args...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// execTestClient implements jobpod.Client over runPodman.
type execTestClient struct{}

func (execTestClient) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	return runPodman(ctx, nil, args...)
}

func (execTestClient) RunStdin(ctx context.Context, stdin []byte, args ...string) ([]byte, []byte, error) {
	return runPodman(ctx, stdin, args...)
}

// skipUnlessExecIntegration is skipUnlessIntegration plus a check that the
// base image is already present — a probe that silently pulls a large
// image on every run is a slow surprise, not a test.
func skipUnlessExecIntegration(t *testing.T) {
	t.Helper()
	skipUnlessIntegration(t)
	if err := exec.Command("podman", "image", "exists", execIntegrationImage).Run(); err != nil {
		t.Skipf("image %s not present; run `podman pull %s` first", execIntegrationImage, execIntegrationImage)
	}
}

// TestExec_Integration_RealPod is the M2-T4b end-to-end probe: a real
// hardened Job Pod is started, code is piped to `python -` on stdin, and
// the exit code / stdout come back through Manager.Exec. It also proves a
// non-zero command exit is a normal ExecResult (not an error), and that
// the `sh -c <command>` shape the run_tests/lint adapter uses works.
func TestExec_Integration_RealPod(t *testing.T) {
	skipUnlessExecIntegration(t)

	mgr := New(execTestClient{}, &stubFreezer{}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const jobID = "7c9e6679-7425-40de-944b-e07fc1f90ae7" // v4 UUID
	if _, err := mgr.Start(ctx, Spec{
		ID:      jobID,
		Image:   execIntegrationImage,
		Command: []string{"sleep", "infinity"},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Stop(context.Background(), jobID) })

	t.Run("success_pipes_code_on_stdin", func(t *testing.T) {
		res, err := mgr.Exec(ctx, jobID, ExecSpec{
			Command: []string{"python", "-"},
			Stdin:   []byte("print('athanor-exec-ok')"),
		})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		if res.ExitCode != 0 {
			t.Errorf("exit = %d, want 0; stderr=%s", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "athanor-exec-ok") {
			t.Errorf("stdout = %q, want it to contain athanor-exec-ok", res.Stdout)
		}
		t.Logf("exec ok: exit=%d stdout=%q duration_ms=%d", res.ExitCode, strings.TrimSpace(res.Stdout), res.DurationMS)
	})

	t.Run("non_zero_exit_is_a_result", func(t *testing.T) {
		res, err := mgr.Exec(ctx, jobID, ExecSpec{
			Command: []string{"python", "-"},
			Stdin:   []byte("raise SystemExit(3)"),
		})
		if err != nil {
			t.Fatalf("Exec returned an error for a non-zero exit: %v", err)
		}
		if res.ExitCode != 3 {
			t.Errorf("exit = %d, want 3", res.ExitCode)
		}
	})

	t.Run("shell_command_shape", func(t *testing.T) {
		res, err := mgr.Exec(ctx, jobID, ExecSpec{
			Command: []string{"sh", "-c", "echo shell-ok"},
		})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		if !strings.Contains(res.Stdout, "shell-ok") {
			t.Errorf("stdout = %q, want shell-ok", res.Stdout)
		}
	})

	// The pod must survive all three execs (a long-lived idle pod), then
	// Stop removes it.
	if _, err := mgr.Get(jobID); err != nil {
		t.Errorf("pod gone after execs: %v", err)
	}
	if err := mgr.Stop(ctx, jobID); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if _, err := mgr.Get(jobID); err == nil {
		t.Error("pod still present after Stop")
	}
}
