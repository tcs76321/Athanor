package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func boolp(v bool) *bool { return &v }

func mkPairedRow(task, arm string, run int, pass bool, candidates int) jobMetrics {
	return jobMetrics{
		GoalName: task, Arm: arm, Run: run, Archetype: "document",
		Suite: "agentic", CheckPass: boolp(pass), Candidates: candidates,
		TokenCost: 100, WallMS: 1000,
	}
}

func TestBuildPairedReportRatesAndLosses(t *testing.T) {
	var rows []jobMetrics
	for run := 1; run <= 3; run++ {
		cascadeCandidates := 1
		if run == 3 {
			cascadeCandidates = 2
		}
		rows = append(rows,
			mkPairedRow("a", "single", run, true, 1),
			mkPairedRow("a", "cascade", run, true, cascadeCandidates),
			mkPairedRow("a", "full", run, true, 3),
			mkPairedRow("b", "single", run, true, 1),
			mkPairedRow("b", "cascade", run, true, 1),
			mkPairedRow("b", "full", run, true, 3),
		)
	}
	r := buildPairedReport(rows, pairedOptions{MarginPoints: 5, MaxTaskLossRuns: 2, Iterations: 2000, Seed: 7})
	if len(r.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(r.Tasks))
	}
	for _, tr := range r.Tasks {
		if tr.SingleRate != 1 || tr.CascadeRate != 1 || tr.FullRate != 1 {
			t.Errorf("%s rates = %+v", tr.Task, tr)
		}
		if tr.CascadeLossRuns != 0 || tr.FullLossRuns != 0 {
			t.Errorf("%s losses = %d/%d", tr.Task, tr.CascadeLossRuns, tr.FullLossRuns)
		}
	}
	if len(r.NonInferiority) != 2 {
		t.Fatalf("non-inferiority rows = %d, want 2", len(r.NonInferiority))
	}
	for _, ni := range r.NonInferiority {
		if !ni.Pass {
			t.Errorf("%s non-inferiority = %+v, want pass", ni.Treatment, ni)
		}
		if ni.MeanDelta != 0 {
			t.Errorf("%s mean delta = %v, want 0", ni.Treatment, ni.MeanDelta)
		}
	}
	if r.EarlyStop.Runs != 6 || r.EarlyStop.EarlyStops != 5 || r.EarlyStop.Escalations != 1 {
		t.Errorf("early stop = %+v, want 6 runs / 5 early / 1 escalation", r.EarlyStop)
	}
	if r.EarlyStop.EarlyStopPassRate != 1 {
		t.Errorf("early-stop pass rate = %v, want 1", r.EarlyStop.EarlyStopPassRate)
	}
}

func TestBuildPairedReportRegressionFailsClaim(t *testing.T) {
	var rows []jobMetrics
	for run := 1; run <= 3; run++ {
		rows = append(rows,
			mkPairedRow("a", "single", run, true, 1),
			mkPairedRow("a", "cascade", run, true, 1),
			mkPairedRow("b", "single", run, true, 1),
			mkPairedRow("b", "cascade", run, false, 3),
		)
	}
	r := buildPairedReport(rows, pairedOptions{MarginPoints: 5, MaxTaskLossRuns: 2, Iterations: 1000, Seed: 3})
	var cascade pairedNonInferiority
	for _, ni := range r.NonInferiority {
		if ni.Treatment == "cascade" {
			cascade = ni
		}
	}
	if cascade.Pass {
		t.Fatalf("cascade claim passed despite %d regressions: %+v", cascade.Regressions, cascade)
	}
	if cascade.Regressions != 1 {
		t.Errorf("regressions = %d, want 1", cascade.Regressions)
	}
	if cascade.MeanDelta >= 0 {
		t.Errorf("mean delta = %v, want negative", cascade.MeanDelta)
	}
}

func TestBootstrapMeanCI(t *testing.T) {
	mean, lo, hi := bootstrapMeanCI([]float64{0.1, 0.2, 0.3}, 5000, 1)
	if mean < 0.19 || mean > 0.21 {
		t.Errorf("mean = %v, want ~0.2", mean)
	}
	if lo <= 0 || hi <= 0 {
		t.Errorf("all-positive values gave CI [%v, %v]", lo, hi)
	}
	_, lo, hi = bootstrapMeanCI([]float64{-0.2, 0.1, 0.0}, 5000, 1)
	if lo >= 0 || hi <= 0 {
		t.Errorf("mixed values gave CI [%v, %v], want it to straddle 0", lo, hi)
	}
	if m, l, h := bootstrapMeanCI(nil, 10, 1); m != 0 || l != 0 || h != 0 {
		t.Errorf("empty CI = %v/%v/%v", m, l, h)
	}
}

func TestRenderPairedMarkdown(t *testing.T) {
	var rows []jobMetrics
	for run := 1; run <= 2; run++ {
		rows = append(rows,
			mkPairedRow("a", "single", run, true, 1),
			mkPairedRow("a", "cascade", run, true, 1),
			mkPairedRow("a", "full", run, true, 3),
		)
	}
	r := buildPairedReport(rows, pairedOptions{MarginPoints: 5, MaxTaskLossRuns: 2, Iterations: 100, Seed: 1})
	md := renderPairedMarkdown(r)
	for _, want := range []string{"Paired v3 report", "Non-inferiority", "cascade", "PASS", "Cascade early stop"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if _, err := pairedJSON(r); err != nil {
		t.Fatalf("pairedJSON: %v", err)
	}
	var round pairedReport
	raw, _ := json.Marshal(r)
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(round.Tasks) != len(r.Tasks) {
		t.Errorf("round-trip tasks = %d, want %d", len(round.Tasks), len(r.Tasks))
	}
}
