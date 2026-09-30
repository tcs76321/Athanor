package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// fakePodExecer records the ExecSpec it received and returns a canned
// result or error.
type fakePodExecer struct {
	n   int
	got jobpod.ExecSpec
	res jobpod.ExecResult
	err error
}

func (f *fakePodExecer) Exec(_ context.Context, _ string, spec jobpod.ExecSpec) (jobpod.ExecResult, error) {
	f.n++
	f.got = spec
	return f.res, f.err
}

func TestPodExecutor_RunCode_StdinAndInterpreter(t *testing.T) {
	f := &fakePodExecer{res: jobpod.ExecResult{ExitCode: 0, Stdout: "1\n"}}
	a := newPodExecutor(f, "alpine:3.20")

	res, err := a.RunCode(context.Background(), "job-1", toolenvelope.ExecuteRequest{Code: "print(1)"})
	if err != nil {
		t.Fatalf("RunCode: %v", err)
	}
	if res.Stdout != "1\n" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "1\n")
	}
	want := []string{"python", "-"}
	if len(f.got.Command) != len(want) || f.got.Command[0] != want[0] || f.got.Command[1] != want[1] {
		t.Errorf("command = %v, want %v", f.got.Command, want)
	}
	if string(f.got.Stdin) != "print(1)" {
		t.Errorf("stdin = %q, want print(1) (code must travel on stdin, not argv)", f.got.Stdin)
	}
}

func TestPodExecutor_RunTests_UsesShell(t *testing.T) {
	f := &fakePodExecer{res: jobpod.ExecResult{ExitCode: 1}}
	a := newPodExecutor(f, "alpine:3.20")

	res, err := a.RunTests(context.Background(), "job-1", toolenvelope.ExecuteRequest{Command: "pytest -q"})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("exit = %d, want 1", res.ExitCode)
	}
	want := []string{"sh", "-c", "pytest -q"}
	if len(f.got.Command) != len(want) {
		t.Fatalf("command = %v, want %v", f.got.Command, want)
	}
	for i := range want {
		if f.got.Command[i] != want[i] {
			t.Errorf("command = %v, want %v", f.got.Command, want)
		}
	}
}

func TestPodExecutor_ErrorTranslation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"no pod", jobpod.ErrNotFound, internalapi.ErrNoPod},
		{"not running", jobpod.ErrNotRunning, internalapi.ErrPodNotRunning},
		{"wrapped no pod", fmt.Errorf("x: %w", jobpod.ErrNotFound), internalapi.ErrNoPod},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newPodExecutor(&fakePodExecer{err: tc.err}, "alpine:3.20")
			_, err := a.Lint(context.Background(), "job-1", toolenvelope.ExecuteRequest{Command: "ruff check ."})
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPodExecutor_EmptyImage_RefusesBeforeExec(t *testing.T) {
	f := &fakePodExecer{}
	a := newPodExecutor(f, "")

	_, err := a.RunCode(context.Background(), "job-1", toolenvelope.ExecuteRequest{Code: "print(1)"})
	if !errors.Is(err, internalapi.ErrExecNotConfigured) {
		t.Errorf("err = %v, want ErrExecNotConfigured", err)
	}
	if f.n != 0 {
		t.Errorf("executor called %d times, want 0 (must refuse before exec)", f.n)
	}
}

func TestPodExecutor_PodmanFailureWrapped(t *testing.T) {
	a := newPodExecutor(&fakePodExecer{err: errors.New("boom")}, "alpine:3.20")

	_, err := a.RunTests(context.Background(), "job-1", toolenvelope.ExecuteRequest{Command: "pytest -q"})
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, internalapi.ErrNoPod) || errors.Is(err, internalapi.ErrPodNotRunning) {
		t.Errorf("err = %v, want a plain wrapped error, not a typed one", err)
	}
}
