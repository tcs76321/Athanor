package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

// TestRun_CodeArchetypeUsesConfiguredTestCommand proves F3-T4: the test
// command the evaluation phase runs comes from the project, not a
// hard-coded `pytest -q`.
func TestRun_CodeArchetypeUsesConfiguredTestCommand(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	jobID := env.submitCode(t)

	j, err := env.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := env.projects.Task(ctx, j.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.projects.SetExecution(ctx, task.ProjectID,
		project.Execution{TestCommand: "go test ./..."}); err != nil {
		t.Fatal(err)
	}

	env.eng.Run(ctx, jobID)

	testCalls := 0
	for _, c := range env.runner.Calls() {
		if c.Tool != "RunTests" {
			continue
		}
		testCalls++
		if c.Cmd != "go test ./..." {
			t.Errorf("RunTests command = %q, want go test ./...", c.Cmd)
		}
	}
	if testCalls == 0 {
		t.Fatal("no RunTests calls; the code evaluation path did not run")
	}
}

// TestRun_DataArchetypePromptIncludesSchema pins the P0 data fix: the schema is
// a pinned prompt requirement, not a dormant chunk the model cannot request.
func TestRun_DataArchetypePromptIncludesSchema(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	projectID, _, jobID := env.createProjectTask(t, "data",
		"Normalize the event list to the provided JSON Schema.")
	if err := env.projects.SetExecution(ctx, projectID, project.Execution{
		DataSchema: `{"type":"array","items":{"type":"object"}}`,
	}); err != nil {
		t.Fatal(err)
	}

	env.eng.Run(ctx, jobID)

	prompts := env.ollama.promptTexts()
	if len(prompts) == 0 {
		t.Fatal("no model call recorded")
	}
	found := false
	for _, p := range prompts {
		if strings.Contains(p, `{"type":"array","items":{"type":"object"}}`) {
			found = true
			break
		}
	}
	if !found {
		t.Error("data schema did not reach the prompt")
	}
}
