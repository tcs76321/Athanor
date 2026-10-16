package engine

import (
	"context"
	"strings"
	"testing"
)

func TestBoundedTestOutput(t *testing.T) {
	// stderr is preferred; a long tail is kept (tracebacks end with the error).
	got := boundedTestOutput("stdout noise", "Traceback\nValueError: bad")
	if !strings.Contains(got, "ValueError: bad") {
		t.Errorf("stderr not captured: %q", got)
	}
	if got := boundedTestOutput("only stdout", ""); got != "only stdout" {
		t.Errorf("stdout fallback = %q", got)
	}
	long := strings.Repeat("x", maxTestOutputChars+500)
	got = boundedTestOutput("", long)
	if len([]rune(got)) > maxTestOutputChars+1 { // +1 for the leading ellipsis
		t.Errorf("output not bounded: %d runes", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("bounded output should be marked as truncated: %q", got)
	}
}

func TestVerifyCandidateCapturesFailureOutput(t *testing.T) {
	env := newEnv(t)
	env.runner.WithResult(1, "Traceback (most recent call last):\nZeroDivisionError: division by zero")
	ctx := context.Background()
	jobID := env.submitCode(t)
	j, err := env.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := env.projects.Get(ctx, j.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := env.projects.Task(ctx, j.TaskID)
	if err != nil {
		t.Fatal(err)
	}

	_, vin, err := env.eng.verifyCandidate(ctx, j, p, task, "def f(): pass", 0)
	if err != nil {
		t.Fatal(err)
	}
	if vin.TestsPassed {
		t.Error("TestsPassed = true for a non-zero exit")
	}
	if !strings.Contains(vin.TestOutput, "ZeroDivisionError") {
		t.Errorf("TestOutput = %q, want the failure traceback (M9-T1)", vin.TestOutput)
	}
}
