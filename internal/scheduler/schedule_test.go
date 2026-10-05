package scheduler

import (
	"reflect"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

func task(id, status, parent string, deps ...string) project.Task {
	return project.Task{ID: id, Status: status, ParentID: parent, DependsOn: deps}
}

func TestLeavesAndReady(t *testing.T) {
	tasks := []project.Task{
		task("A", project.TaskPending, ""),
		task("B", project.TaskPending, "", "A"),
		task("C", project.TaskPending, "", "A"),
		task("D", project.TaskPending, "", "B", "C"),
	}
	if got := Leaves(tasks); !reflect.DeepEqual(got, []string{"A", "B", "C", "D"}) {
		t.Fatalf("Leaves = %v", got)
	}
	if got := Ready(tasks); !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("initial Ready = %v, want [A]", got)
	}

	tasks[0].Status = project.TaskCompleted
	if got := Ready(tasks); !reflect.DeepEqual(got, []string{"B", "C"}) {
		t.Fatalf("after A Ready = %v, want [B C]", got)
	}

	tasks[1].Status = project.TaskCompleted
	tasks[2].Status = project.TaskCompleted
	if got := Ready(tasks); !reflect.DeepEqual(got, []string{"D"}) {
		t.Fatalf("after B,C Ready = %v, want [D]", got)
	}
}

func TestReadySkipsParents(t *testing.T) {
	tasks := []project.Task{
		task("P", project.TaskPending, ""),
		task("L1", project.TaskPending, "P"),
		task("L2", project.TaskPending, "P", "L1"),
	}
	if got := Leaves(tasks); !reflect.DeepEqual(got, []string{"L1", "L2"}) {
		t.Fatalf("Leaves = %v, want the leaves only", got)
	}
	if got := Ready(tasks); !reflect.DeepEqual(got, []string{"L1"}) {
		t.Fatalf("Ready = %v, want [L1]", got)
	}
}

func TestBlockedClosure(t *testing.T) {
	tasks := []project.Task{
		task("A", project.TaskFailed, ""),
		task("B", project.TaskPending, "", "A"),
		task("C", project.TaskPending, "", "B"),
		task("D", project.TaskPending, "", "A"),   // sibling, also blocked
		task("E", project.TaskCompleted, "", "A"), // completed stays completed
		task("R", project.TaskRunning, "", "A"),   // already running is untouched
		task("X", project.TaskPending, ""),        // independent
	}
	want := []string{"B", "C", "D"}
	if got := Blocked(tasks); !reflect.DeepEqual(got, want) {
		t.Fatalf("Blocked = %v, want %v", got, want)
	}
}

func TestBlockedNoFailureIsEmpty(t *testing.T) {
	tasks := []project.Task{
		task("A", project.TaskCompleted, ""),
		task("B", project.TaskPending, "", "A"),
	}
	if got := Blocked(tasks); len(got) != 0 {
		t.Fatalf("Blocked = %v, want none", got)
	}
}

func TestReadyMissingDepIsNotReady(t *testing.T) {
	tasks := []project.Task{task("A", project.TaskPending, "", "ghost")}
	if got := Ready(tasks); len(got) != 0 {
		t.Fatalf("Ready = %v, want none for an unresolvable dependency", got)
	}
}
