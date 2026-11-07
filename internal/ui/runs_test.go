package ui

import (
	"fmt"
	"testing"
	"time"
)

func TestPruneRunsDropsOldestCompleted(t *testing.T) {
	base := time.Unix(1000, 0)
	runs := map[string]*indexRun{}
	for i := 0; i < 5; i++ {
		runs[fmt.Sprintf("p%d", i)] = &indexRun{
			Status:    "done",
			Done:      true,
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		}
	}
	pruneRuns(runs, 3)
	if len(runs) != 3 {
		t.Fatalf("len = %d, want 3 (%v)", len(runs), keys(runs))
	}
	for _, kept := range []string{"p2", "p3", "p4"} {
		if runs[kept] == nil {
			t.Errorf("newest entry %q was pruned", kept)
		}
	}
	for _, dropped := range []string{"p0", "p1"} {
		if runs[dropped] != nil {
			t.Errorf("oldest entry %q survived", dropped)
		}
	}
}

func TestPruneRunsNeverDropsRunning(t *testing.T) {
	base := time.Unix(1000, 0)
	runs := map[string]*indexRun{
		"old-running": {Status: "running", StartedAt: base},
		"old-done":    {Status: "done", Done: true, StartedAt: base.Add(time.Minute)},
		"new-done":    {Status: "done", Done: true, StartedAt: base.Add(2 * time.Minute)},
	}
	pruneRuns(runs, 2)
	if runs["old-running"] == nil {
		t.Fatal("a running entry was pruned")
	}
	if runs["new-done"] == nil {
		t.Fatal("the newest completed entry was pruned")
	}
	if runs["old-done"] != nil {
		t.Fatal("the oldest completed entry survived")
	}
}

func TestPruneRunsBelowLimitIsNoop(t *testing.T) {
	runs := map[string]*indexRun{
		"a": {Status: "done", Done: true},
	}
	pruneRuns(runs, 10)
	if len(runs) != 1 {
		t.Fatalf("len = %d, want 1", len(runs))
	}
}

func TestPruneRunsTieIsDeterministic(t *testing.T) {
	at := time.Unix(1000, 0)
	runs := map[string]*indexRun{
		"b": {Status: "done", Done: true, StartedAt: at},
		"a": {Status: "done", Done: true, StartedAt: at},
		"c": {Status: "done", Done: true, StartedAt: at},
	}
	pruneRuns(runs, 2)
	if runs["a"] != nil || runs["c"] == nil || runs["b"] == nil {
		t.Fatalf("tie did not prune the lowest key first: %v", keys(runs))
	}
}

func keys(m map[string]*indexRun) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
