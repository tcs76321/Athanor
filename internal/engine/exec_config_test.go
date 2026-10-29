package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
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

// TestRun_DataArchetypeAcceptsWrappedJSON is the end-to-end data proof: the
// schema reaches the prompt, the verifier extracts a single fenced value, and
// the job completes without a judge-only accept.
func TestRun_DataArchetypeAcceptsWrappedJSON(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	projectID, _, jobID := env.createProjectTask(t, "data",
		"Normalize the event list to the provided JSON Schema.")
	if err := env.projects.SetExecution(ctx, projectID, project.Execution{
		DataSchema: `{"type":"array","items":{"type":"object","required":["id"],"additionalProperties":false,"properties":{"id":{"type":"string"}}}}`,
	}); err != nil {
		t.Fatal(err)
	}
	wrapped := "Here is the normalized output:\n```json\n[{\"id\":\"a\"}]\n```"
	env.ollama.setGeneric(wrapped)
	env.ollama.setSynthesis(wrapped)

	env.eng.Run(ctx, jobID)

	j, err := env.jobs.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed (data candidate validates)", j.State)
	}
	a, err := env.artifacts.LatestForJob(ctx, jobID, artifact.KindDataset)
	if err != nil {
		t.Fatalf("loading final dataset artifact: %v", err)
	}
	if a.Status != artifact.StatusAccepted {
		t.Errorf("artifact status = %s, want accepted", a.Status)
	}
}
