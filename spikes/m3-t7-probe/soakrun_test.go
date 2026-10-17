package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMetricRowsRoundTrip proves the M8-T23 resume substrate: metrics.jsonl is
// append-only and reads back for a resumed run.
func TestMetricRowsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	if rows, err := loadMetricRows(path); err != nil || rows != nil {
		t.Fatalf("missing file: rows=%v err=%v, want nil,nil", rows, err)
	}
	m := jobMetrics{GoalName: "g", Arm: "full", ModelLabel: "m1", State: "completed", Winner: "new", Tier: "H"}
	if err := appendMetricRow(path, m); err != nil {
		t.Fatal(err)
	}
	if err := appendMetricRow(path, m); err != nil {
		t.Fatal(err)
	}
	rows, err := loadMetricRows(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].GoalName != "g" || rows[0].Tier != "H" {
		t.Errorf("rows = %+v, want two rows with tier H", rows)
	}
}

func TestNamedArm(t *testing.T) {
	for _, name := range []string{"single", "dialectical", "bestof3", "reflect", "full"} {
		if _, ok := namedArm(name); !ok {
			t.Errorf("namedArm(%q) not found", name)
		}
	}
	if _, ok := namedArm("nope"); ok {
		t.Error("namedArm(\"nope\") found, want miss")
	}
}

// TestPassAllowed pins the full-slice contract: -passes 1 starts exactly one
// pass even when the wall-time budget has room, and a positive cap wins over a
// passed deadline (the pass itself is bounded by the per-job timeout).
func TestPassAllowed(t *testing.T) {
	now := time.Now()
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	cases := []struct {
		name        string
		pass, max   int
		deadline    time.Time
		interrupted bool
		want        bool
	}{
		{"first of one", 0, 1, future, false, true},
		{"second of one", 1, 1, future, false, false},
		{"deadline open", 0, 0, future, false, true},
		{"deadline passed", 0, 0, past, false, false},
		{"interrupted", 0, 0, future, true, false},
		{"cap beats deadline", 0, 2, past, false, true},
	}
	for _, c := range cases {
		if got := passAllowed(c.pass, c.max, c.deadline, now, c.interrupted); got != c.want {
			t.Errorf("%s: passAllowed = %v, want %v", c.name, got, c.want)
		}
	}

	// A capped run hard-stops between tasks at the deadline; an uncapped
	// endurance run finishes its pass and checks the deadline between passes.
	if !midPassDeadline(1, past, now) {
		t.Error("capped run did not hard-stop at a passed deadline")
	}
	if midPassDeadline(1, future, now) {
		t.Error("capped run stopped before its deadline")
	}
	if midPassDeadline(0, past, now) {
		t.Error("uncapped endurance run was cut mid-pass")
	}
}

// TestSoakCompleteMarker pins the resume rule: only a normal-completion marker
// counts. report.md is written after every pass (including an interrupted one),
// so it must not make the ladder skip a partial model.
func TestSoakCompleteMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if soakComplete(dir) {
		t.Error("report.md must not count as a complete soak")
	}
	if err := writeComplete(dir, 3, 1); err != nil {
		t.Fatal(err)
	}
	if !soakComplete(dir) {
		t.Error("complete marker not detected")
	}
}
