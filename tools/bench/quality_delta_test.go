package main

import (
	"strings"
	"testing"
)

func qScore(goal, arm string, run int, judge string, overall float64) qualityScore {
	return qualityScore{GoalName: goal, ModelLabel: "bonsai27b", Arm: arm, Run: run, Judge: judge, Overall: overall}
}

func TestBuildQualityDeltaPass(t *testing.T) {
	var scores []qualityScore
	for _, goal := range []string{"a", "b", "c"} {
		for run := 1; run <= 3; run++ {
			scores = append(scores,
				qScore(goal, "single", run, "j1", 3.0),
				qScore(goal, "single", run, "j2", 3.0),
				qScore(goal, "full", run, "j1", 3.6),
				qScore(goal, "full", run, "j2", 3.6),
			)
		}
	}
	r := buildQualityDelta(qualityReport{Judges: []string{"j1", "j2"}, Scores: scores}, 0.5, 0.2, 0.6, 1000, 1)
	if len(r.Generators) != 1 {
		t.Fatalf("generators = %d, want 1", len(r.Generators))
	}
	g := r.Generators[0]
	if g.Delta.MeanDelta < 0.59 || g.Delta.MeanDelta > 0.61 {
		t.Errorf("mean delta = %v, want 0.6", g.Delta.MeanDelta)
	}
	if g.Delta.WinRate != 1 {
		t.Errorf("win rate = %v, want 1", g.Delta.WinRate)
	}
	if !g.Delta.Pass {
		t.Errorf("claim failed: %+v", g.Delta)
	}
	if g.Tasks != 3 || g.Delta.Pairs != 9 {
		t.Errorf("tasks/pairs = %d/%d, want 3/9", g.Tasks, g.Delta.Pairs)
	}
}

func TestBuildQualityDeltaFail(t *testing.T) {
	var scores []qualityScore
	for _, goal := range []string{"a", "b", "c"} {
		for run := 1; run <= 2; run++ {
			scores = append(scores,
				qScore(goal, "single", run, "j1", 3.0),
				qScore(goal, "full", run, "j1", 2.8),
			)
		}
	}
	r := buildQualityDelta(qualityReport{Judges: []string{"j1"}, Scores: scores}, 0.5, 0.2, 0.6, 1000, 1)
	if len(r.Generators) != 1 {
		t.Fatalf("generators = %d, want 1", len(r.Generators))
	}
	if r.Generators[0].Delta.Pass {
		t.Fatalf("claim passed with a negative delta: %+v", r.Generators[0].Delta)
	}
	if r.Generators[0].Delta.MeanDelta > 0 {
		t.Errorf("mean delta = %v, want negative", r.Generators[0].Delta.MeanDelta)
	}
}

func TestRenderQualityDeltaEmpty(t *testing.T) {
	md := renderQualityDelta(qualityDeltaReport{})
	if !strings.Contains(md, "No paired quality artifacts") {
		t.Errorf("empty render = %q", md)
	}
}
