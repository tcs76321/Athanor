package main

import "testing"

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
