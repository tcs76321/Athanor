package project

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/dag"
)

func TestCreateDAGPersistsGraph(t *testing.T) {
	r, s := openRepo(t)
	ctx := context.Background()
	p, _, err := r.Create(ctx, "demo", ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	specs := []TaskSpec{
		{Key: "phase", Title: "Phase 1", Description: "group"},
		{Key: "schema", ParentKey: "phase", Title: "Schema", Criteria: []string{"schema.sql exists"},
			Budget: dag.Budget{MaxJobs: 1, MaxLLMCalls: 4, MaxTokens: 8000, MaxWallTime: 20 * time.Minute}},
		{Key: "impl", ParentKey: "phase", Title: "Implement", DependsOn: []string{"schema"}, Criteria: []string{"tests pass"}},
	}
	goalID, tasks, err := r.CreateDAG(ctx, p.ID, "Build a small REST endpoint with tests.", []string{"all tests green"}, specs)
	if err != nil {
		t.Fatalf("CreateDAG: %v", err)
	}
	if goalID == "" || len(tasks) != 3 {
		t.Fatalf("goalID=%q tasks=%d", goalID, len(tasks))
	}

	var goalStatus string
	if err := s.DB().QueryRow(`SELECT status FROM goals WHERE id = ?`, goalID).Scan(&goalStatus); err != nil {
		t.Fatal(err)
	}
	if goalStatus != "decomposed" {
		t.Errorf("goal status = %q, want decomposed", goalStatus)
	}

	byKey := map[string]Task{}
	for _, task := range tasks {
		if task.GoalID != goalID || task.ProjectID != p.ID || task.Status != "pending" {
			t.Errorf("task %s = %+v", task.Title, task)
		}
		byKey[task.Title] = task
	}
	schema, impl := byKey["Schema"], byKey["Implement"]
	if schema.ParentID == "" || schema.ParentID != byKey["Phase 1"].ID {
		t.Errorf("schema parent = %q, phase id = %q", schema.ParentID, byKey["Phase 1"].ID)
	}
	if len(impl.DependsOn) != 1 || impl.DependsOn[0] != schema.ID {
		t.Errorf("impl deps = %v, schema id = %q", impl.DependsOn, schema.ID)
	}
	if schema.Budget.MaxJobs != 1 || schema.Budget.MaxWallTime != 20*time.Minute {
		t.Errorf("schema budget = %+v", schema.Budget)
	}
}

func TestCreateDAGRejectsBadSpecs(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()
	p, _, err := r.Create(ctx, "demo", ArchetypeText, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		specs []TaskSpec
	}{
		{"empty", nil},
		{"duplicate key", []TaskSpec{{Key: "a", Title: "a"}, {Key: "a", Title: "b"}}},
		{"unknown parent", []TaskSpec{{Key: "a", ParentKey: "ghost", Title: "a"}}},
		{"unknown dep", []TaskSpec{{Key: "a", DependsOn: []string{"ghost"}, Title: "a"}}},
		{"empty key", []TaskSpec{{Key: "", Title: "a"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := r.CreateDAG(ctx, p.ID, validGoal, nil, c.specs); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestCreateDAGMissingProject(t *testing.T) {
	r, _ := openRepo(t)
	_, _, err := r.CreateDAG(context.Background(), "nope", validGoal, nil,
		[]TaskSpec{{Key: "a", Title: "a"}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTasksByGoalUnknownIsEmpty(t *testing.T) {
	r, _ := openRepo(t)
	tasks, err := r.TasksByGoal(context.Background(), "nope")
	if err != nil {
		t.Fatalf("TasksByGoal: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %v, want none", tasks)
	}
}
