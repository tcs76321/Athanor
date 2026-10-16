// Package scheduler executes a decomposed task graph (ARCHITECTURE §7.2,
// ROADMAP M6-T2; ADR-0033).
//
// The pure functions here — Leaves, Ready, Blocked — decide what the graph
// says should run next, with no I/O; the Scheduler type wires them to the
// project repository, the job repository, and the engine's enqueue surface.
// Parent (phase) tasks are grouping only and never receive a job; scheduling
// is evaluated over leaves.
package scheduler

import (
	"sort"

	"github.com/tcs76321/athanor/internal/project"
)

// Leaves returns the sorted IDs of tasks that are nobody's parent — the
// tasks that are eligible to run.
func Leaves(tasks []project.Task) []string {
	parents := parentSet(tasks)
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if !parents[t.ID] {
			out = append(out, t.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Ready returns the sorted IDs of leaf tasks that are `pending` or `ready` and
// whose every dependency is `completed`. Missing dependencies (which the M6-T1
// validator rejects before persistence) leave a task not-ready. `ready` is
// accepted for parity with `Blocked` (known-issues M19): both treat it as a
// schedulable, non-terminal status even though nothing writes it today.
func Ready(tasks []project.Task) []string {
	byID := index(tasks)
	parents := parentSet(tasks)
	var out []string
	for _, t := range tasks {
		if parents[t.ID] || (t.Status != project.TaskPending && t.Status != project.TaskReady) {
			continue
		}
		if depsCompleted(t, byID) {
			out = append(out, t.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Blocked returns the sorted IDs of non-terminal tasks that depend,
// directly or transitively, on a `failed` or already-`blocked` task. It is
// the §7.2 "descendants are marked blocked" closure, computed to a fixpoint.
func Blocked(tasks []project.Task) []string {
	byID := index(tasks)
	blocked := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, t := range tasks {
			if blocked[t.ID] || (t.Status != project.TaskPending && t.Status != project.TaskReady) {
				continue
			}
			for _, dep := range t.DependsOn {
				d, ok := byID[dep]
				if !ok {
					continue
				}
				if d.Status == project.TaskFailed || d.Status == project.TaskBlocked || blocked[dep] {
					blocked[t.ID] = true
					changed = true
					break
				}
			}
		}
	}
	out := make([]string, 0, len(blocked))
	for id := range blocked {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// dependents returns the sorted IDs of tasks that depend on root, directly
// or transitively (the reverse of the dependency edges, to a fixpoint). It
// excludes root itself. Used by the operator Reset seam (known-issues H8) to
// find the subtree to return to `pending`.
func dependents(tasks []project.Task, root string) []string {
	inSubtree := map[string]bool{root: true}
	for changed := true; changed; {
		changed = false
		for _, t := range tasks {
			if inSubtree[t.ID] {
				continue
			}
			for _, dep := range t.DependsOn {
				if inSubtree[dep] {
					inSubtree[t.ID] = true
					changed = true
					break
				}
			}
		}
	}
	out := make([]string, 0, len(inSubtree)-1)
	for id := range inSubtree {
		if id != root {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// depsCompleted reports whether every dependency of t exists and is
// completed.
func depsCompleted(t project.Task, byID map[string]project.Task) bool {
	for _, dep := range t.DependsOn {
		d, ok := byID[dep]
		if !ok || d.Status != project.TaskCompleted {
			return false
		}
	}
	return true
}

// parentSet returns the set of task IDs that are referenced as a parent.
func parentSet(tasks []project.Task) map[string]bool {
	parents := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.ParentID != "" {
			parents[t.ParentID] = true
		}
	}
	return parents
}

// index maps task ID → task.
func index(tasks []project.Task) map[string]project.Task {
	byID := make(map[string]project.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	return byID
}
