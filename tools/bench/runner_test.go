package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProjectCreateBodyDisablesAutoStart pins the M9-T5 interaction: the
// probe submits its goal explicitly, so project create must not also enqueue
// the same goal. Without start:false every benchmark task runs twice.
func TestProjectCreateBodyDisablesAutoStart(t *testing.T) {
	body := projectCreateBody(sampleGoal{
		Archetype: "code",
		Goal:      "write a function",
		Criteria:  []string{"pure stdlib"},
	}, "task-name")
	if body["start"] != false {
		t.Errorf("start = %v, want false (project create must not enqueue a second job)", body["start"])
	}
	if body["name"] != "task-name" || body["goal"] != "write a function" {
		t.Errorf("body = %+v, want the task name and goal", body)
	}
}

// TestDataSchemaForFailsLoudly pins P0-2: a declared but missing schema is a
// run error, not a silently disabled verifier.
func TestDataSchemaForFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schema, []byte(`{"type":"array"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	g := sampleGoal{Checks: map[string]any{"json_schema": schema}}
	got, err := dataSchemaFor(g)
	if err != nil {
		t.Fatalf("dataSchemaFor: %v", err)
	}
	if got != `{"type":"array"}` {
		t.Errorf("schema = %q", got)
	}

	g.Checks["json_schema"] = filepath.Join(dir, "missing.json")
	if _, err := dataSchemaFor(g); err == nil {
		t.Error("missing schema accepted, want an error")
	}
}
