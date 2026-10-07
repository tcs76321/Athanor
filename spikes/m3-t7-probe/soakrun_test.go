package main

import (
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
