package main

import (
	"strings"
	"testing"
)

// mk builds a completed synthetic job row for the report tests.
func mk(goal, arm string, run int, score, conf, div float64, winner string, tokens int) jobMetrics {
	return jobMetrics{
		GoalName: goal, Archetype: "code", Arm: arm, Run: run, State: "completed",
		Score: score, Confidence: conf, Diversity: div, Winner: winner, TokenCost: tokens,
	}
}

func TestHeadlineTable(t *testing.T) {
	metrics := []jobMetrics{
		mk("a", "single", 1, 0.4, 0.5, 0, "new", 100),
		mk("a", "dialectical", 1, 0.6, 0.7, 0.8, "new", 300),
		mk("a", "dialectical", 2, 0.8, 0.7, 0.6, "new", 300),
		mk("b", "single", 1, 0.9, 0.9, 0, "new", 100),
		mk("b", "dialectical", 1, 0.5, 0.6, 0.7, "previous", 250),
	}
	rows := headlineTable(metrics)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].Goal != "a" || rows[1].Goal != "b" {
		t.Errorf("rows not sorted by goal: %+v", rows)
	}
	if !almost(rows[0].SingleScore, 0.4) {
		t.Errorf("single score = %v, want 0.4", rows[0].SingleScore)
	}
	if !almost(rows[0].DialecticalScore, 0.7) {
		t.Errorf("dialectical mean = %v, want 0.7", rows[0].DialecticalScore)
	}
	if !almost(rows[0].Delta, 0.3) {
		t.Errorf("delta = %v, want 0.3", rows[0].Delta)
	}
	if rows[0].DialecticalTokens != 300 {
		t.Errorf("dialectical tokens = %v, want 300", rows[0].DialecticalTokens)
	}
}

func TestCalibrationPairs(t *testing.T) {
	metrics := []jobMetrics{
		mk("a", "dialectical", 1, 0.8, 0.7, 0, "new", 1),
		mk("a", "single", 1, 0.4, 0.5, 0, "new", 1),         // excluded (single arm)
		{GoalName: "b", Arm: "dialectical", State: "error"}, // excluded
	}
	pairs := calibrationPairs(metrics)
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1", len(pairs))
	}
	if !almost(pairs[0].Confidence, 0.7) || !almost(pairs[0].Observed, 0.8) {
		t.Errorf("pair = %+v, want {0.7 0.8}", pairs[0])
	}
}

func TestStabilityTally(t *testing.T) {
	metrics := []jobMetrics{
		mk("a", "dialectical", 1, 1, 1, 0, "new", 1),
		mk("a", "dialectical", 2, 1, 1, 0, "new", 1),
		mk("a", "dialectical", 3, 1, 1, 0, "new", 1),
		mk("b", "dialectical", 1, 1, 1, 0, "new", 1),
		mk("b", "dialectical", 2, 1, 1, 0, "previous", 1),
		mk("b", "dialectical", 3, 1, 1, 0, "new", 1),
		mk("c", "dialectical", 1, 1, 1, 0, "new", 1),
		mk("c", "dialectical", 2, 1, 1, 0, "previous", 1),
		mk("c", "dialectical", 3, 1, 1, 0, "none", 1),
	}
	tally := stabilityTally(metrics)
	if tally["3-same"] != 1 || tally["2-1"] != 1 || tally["3-way"] != 1 {
		t.Errorf("tally = %+v, want one of each", tally)
	}
}

func TestMeanDiversity(t *testing.T) {
	metrics := []jobMetrics{
		mk("a", "dialectical", 1, 1, 1, 0.8, "new", 1),
		mk("a", "dialectical", 2, 1, 1, 0.4, "new", 1),
		mk("b", "single", 1, 1, 1, 0, "new", 1),
	}
	if got := meanDiversity(metrics, "dialectical"); !almost(got, 0.6) {
		t.Errorf("dialectical diversity = %v, want 0.6", got)
	}
	if got := meanDiversity(metrics, "single"); got != 0 {
		t.Errorf("single diversity = %v, want 0", got)
	}
}

func TestRenderReport(t *testing.T) {
	out := renderReport([]jobMetrics{
		mk("a", "single", 1, 0.4, 0.5, 0, "new", 100),
		mk("a", "dialectical", 1, 0.6, 0.7, 0.5, "new", 300),
	})
	for _, want := range []string{"Headline", "T-b", "T-c", "T-a", "Mean pairwise Jaccard", "a | code"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n%s", want, out)
		}
	}
}

// TestVerificationSummary pins the F4-T3 deterministic-accept counting.
func TestVerificationSummary(t *testing.T) {
	yes, no := true, false
	metrics := []jobMetrics{
		{Archetype: "code", Winner: "new", State: "completed", JudgeCalled: &no},
		{Archetype: "code", Winner: "new", State: "completed", JudgeCalled: &yes},
		{Archetype: "code", Winner: "none", State: "failed", JudgeCalled: &yes},
		{Archetype: "text", Winner: "new", State: "completed", JudgeCalled: &yes},
	}
	accepts, det := verificationSummary(metrics)
	if accepts != 2 || det != 1 {
		t.Fatalf("verificationSummary = %d accepts / %d deterministic, want 2/1", accepts, det)
	}
}
