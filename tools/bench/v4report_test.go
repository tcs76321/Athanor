package main

import (
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// TestBuildV4ReportJoinsAndEvaluates pins the paired join: the same task is
// compared across arms, the cycle must not fall more than the margin below the
// baseline, and citation checks are required for the utility floor.
func TestBuildV4ReportJoinsAndEvaluates(t *testing.T) {
	base := []jobMetrics{
		{GoalName: "v4-brief-sqlite", Arm: "single", CheckPass: boolPtr(false)},
		{GoalName: "v4-brief-wal", Arm: "single", CheckPass: boolPtr(true)},
	}
	cycle := cycleResults{
		Model: "bonsai27b",
		Tasks: []cycleTaskResult{
			{ID: "v4-brief-sqlite", Pass: true, State: "completed", Checks: []checkResult{
				{Kind: "allowed_urls", Pass: true},
				{Kind: "cite_every_paragraph", Pass: true},
				{Kind: "must_contain", Pass: true},
			}},
			{ID: "v4-brief-wal", Pass: false, State: "completed", Failures: []string{"cite_every_paragraph: uncited=1/2"}},
		},
	}
	rep := buildV4Report(v4ReportInput{baseline: base, cycle: cycle, model: "bonsai27b", margin: 5})
	if len(rep.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(rep.Tasks))
	}
	if rep.BaselinePassRate != 0.5 || rep.CyclePassRate != 0.5 {
		t.Errorf("rates = %.2f/%.2f", rep.BaselinePassRate, rep.CyclePassRate)
	}
	if rep.DeltaPoints != 0 || !rep.NonInferior {
		t.Errorf("delta = %.1f non-inferior=%v", rep.DeltaPoints, rep.NonInferior)
	}
	if rep.CitationPassRate != 0.5 {
		t.Errorf("citation rate = %.2f, want 0.5", rep.CitationPassRate)
	}
	if rep.Sufficient {
		t.Error("utility floor should fail at 50%")
	}
}

func TestBuildV4ReportNeverWorseMargin(t *testing.T) {
	base := []jobMetrics{
		{GoalName: "a", Arm: "single", CheckPass: boolPtr(true)},
		{GoalName: "b", Arm: "single", CheckPass: boolPtr(true)},
	}
	cycle := cycleResults{Tasks: []cycleTaskResult{
		{ID: "a", Pass: true}, {ID: "b", Pass: false},
	}}
	rep := buildV4Report(v4ReportInput{baseline: base, cycle: cycle, model: "m", margin: 5})
	if rep.NonInferior {
		t.Errorf("50%% vs 100%% should violate a 5-point margin: delta=%.1f", rep.DeltaPoints)
	}
}

func TestCitationChecksPassed(t *testing.T) {
	ok := []checkResult{
		{Kind: "allowed_urls", Pass: true},
		{Kind: "cite_every_paragraph", Pass: true},
		{Kind: "must_contain", Pass: true},
	}
	if !citationChecksPassed(ok) {
		t.Error("all citation checks should pass")
	}
	if citationChecksPassed([]checkResult{{Kind: "allowed_urls", Pass: true}}) {
		t.Error("missing citation checks should fail")
	}
	if citationChecksPassed(append(ok, checkResult{Kind: "cite_every_paragraph", Pass: false})) {
		t.Error("a failed citation check should fail")
	}
}
