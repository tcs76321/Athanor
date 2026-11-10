package contextasm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

// stubRuntime satisfies Runtime with nil seams; only Dependencies is set.
type stubRuntime struct {
	deps Dependencies
}

func (s stubRuntime) Chunks() Provider                                         { return nil }
func (s stubRuntime) Evictor() Evictor                                         { return nil }
func (s stubRuntime) Corrections() Corrections                                 { return nil }
func (s stubRuntime) Memory() Memory                                           { return nil }
func (s stubRuntime) Insights() Insights                                       { return nil }
func (s stubRuntime) Interruptions() Interruptions                             { return nil }
func (s stubRuntime) Notebook() Notebook                                       { return nil }
func (s stubRuntime) Planner() Planner                                         { return nil }
func (s stubRuntime) Dependencies() Dependencies                               { return s.deps }
func (s stubRuntime) AuditCat(context.Context, string, string, map[string]any) {}

type fakeDeps struct {
	outputs []DependencyOutput
	err     error
	project string
	tasks   []string
}

func (f *fakeDeps) TaskOutputs(_ context.Context, projectID string, taskIDs []string) ([]DependencyOutput, error) {
	f.project, f.tasks = projectID, taskIDs
	return f.outputs, f.err
}

func TestWithUpstreamAppendsDependencyOutputs(t *testing.T) {
	deps := &fakeDeps{outputs: []DependencyOutput{
		{TaskID: "research", Title: "Game Direction Research", Content: "three candidate directions"},
	}}
	a := &Assembler{rt: stubRuntime{deps: deps}}
	task := project.Task{ID: "options", DependsOn: []string{"research"}, Description: "Generate three options."}

	got := a.withUpstream(context.Background(), PrepareRequest{Project: project.Project{ID: "p1"}}, task)
	if deps.project != "p1" || len(deps.tasks) != 1 || deps.tasks[0] != "research" {
		t.Errorf("read = %q/%v", deps.project, deps.tasks)
	}
	if !strings.Contains(got.Description, "Generate three options.") {
		t.Error("original description lost")
	}
	if !strings.Contains(got.Description, "UPSTREAM RESULT — Game Direction Research") ||
		!strings.Contains(got.Description, "three candidate directions") {
		t.Errorf("description = %q", got.Description)
	}
}

func TestWithUpstreamLeavesTaskWhenThereIsNoData(t *testing.T) {
	task := project.Task{ID: "x", DependsOn: []string{"a"}, Description: "do it"}

	a := &Assembler{} // nil runtime
	if got := a.withUpstream(context.Background(), PrepareRequest{}, task); got.Description != task.Description {
		t.Errorf("nil runtime changed description: %q", got.Description)
	}

	a = &Assembler{rt: stubRuntime{deps: &fakeDeps{err: errors.New("db down")}}}
	if got := a.withUpstream(context.Background(), PrepareRequest{}, task); got.Description != task.Description {
		t.Errorf("error changed description: %q", got.Description)
	}

	a = &Assembler{rt: stubRuntime{deps: &fakeDeps{outputs: []DependencyOutput{{TaskID: "a", Title: "A", Content: "  "}}}}}
	if got := a.withUpstream(context.Background(), PrepareRequest{}, task); got.Description != task.Description {
		t.Errorf("empty output changed description: %q", got.Description)
	}

	noDeps := project.Task{ID: "x", Description: "do it"}
	deps := &fakeDeps{outputs: []DependencyOutput{{TaskID: "a", Title: "A", Content: "ignored"}}}
	a = &Assembler{rt: stubRuntime{deps: deps}}
	if got := a.withUpstream(context.Background(), PrepareRequest{}, noDeps); got.Description != noDeps.Description {
		t.Errorf("no dependencies changed description: %q", got.Description)
	}
	if deps.project != "" {
		t.Error("read called without dependencies")
	}
}

func TestWithUpstreamCapsOneBlock(t *testing.T) {
	long := strings.Repeat("x", maxUpstreamBlockChars+500)
	deps := &fakeDeps{outputs: []DependencyOutput{{TaskID: "a", Title: "Big", Content: long}}}
	a := &Assembler{rt: stubRuntime{deps: deps}}
	got := a.withUpstream(context.Background(), PrepareRequest{Project: project.Project{ID: "p1"}}, project.Task{ID: "b", DependsOn: []string{"a"}, Description: "next"})
	if len(got.Description) > len("next")+maxUpstreamBlockChars+600 {
		t.Errorf("description length = %d, not capped", len(got.Description))
	}
	if !strings.Contains(got.Description, "…") {
		t.Error("truncation marker missing")
	}
}
