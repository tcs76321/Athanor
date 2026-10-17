package main

import (
	"os"
	"path/filepath"
	"testing"
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
