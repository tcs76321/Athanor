package engine

import (
	"context"
	"strings"
	"testing"
)

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

	_, vin, err := env.eng.phaseDeps.VerifyCandidate(ctx, j, p, task, "def f(): pass", 0)
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
