package main

import (
	"strings"
	"testing"
)

// mk builds a completed synthetic job row for the report tests.
func mk(goal, arm string, run int, score, conf, div float64, winner string, tokens int) jobMetrics {
	return jobMetrics{
		ModelLabel: "m1", Family: "fam",
		GoalName: goal, Archetype: "code", Arm: arm, Run: run, State: "completed",
		Score: score, Confidence: conf, Diversity: div, Winner: winner, TokenCost: tokens,
	}
}

func TestCorpusSummary(t *testing.T) {
	pass, fail := true, false
	metrics := []jobMetrics{
		{Tier: "H", Arm: "single", State: "completed", Winner: "new", CheckPass: &pass},
		{Tier: "H", Arm: "single", State: "completed", Winner: "new", CheckPass: &fail},
		{Tier: "H", Arm: "full", State: "completed", Winner: "new"}, // code: engine accept
		{Tier: "M", Arm: "full", State: "failed", Winner: "none"},
	}
	rows := corpusSummary(metrics)
	byKey := map[string]tierPassRow{}
	for _, r := range rows {
		byKey[r.Tier+"/"+r.Arm] = r
	}
	if len(rows) != 3 {
		t.Fatalf("cells = %+v, want 3", rows)
	}
	if r := byKey["H/single"]; r.N != 2 || r.Pass != 1 {
		t.Errorf("H/single = %+v, want 1/2", r)
	}
	if r := byKey["H/full"]; r.N != 1 || r.Pass != 1 {
		t.Errorf("H/full = %+v, want 1/1 (engine accept)", r)
	}
	if r := byKey["M/full"]; r.N != 1 || r.Pass != 0 {
		t.Errorf("M/full = %+v, want 0/1", r)
	}
}

// TestCodeTestOutcomeIsTheLabel is the ADR-0092 integrity test: a code row is
// labeled by the recorded frozen-test outcome, not by the engine's accept. A
// test-passing candidate the engine rejected is a capability pass; a completed
// engine accept whose candidate failed the test is a capability failure.
func TestCodeTestOutcomeIsTheLabel(t *testing.T) {
	pass, fail := true, false
	rows := []jobMetrics{
		{Archetype: "code", Tier: "X", Arm: "full", State: "failed", Winner: "none", CodeTestPassed: &pass},
		{Archetype: "code", Tier: "X", Arm: "full", State: "completed", Winner: "new", CodeTestPassed: &fail},
	}
	if !jobPassed(rows[0]) {
		t.Error("a code row with a passing recorded test must pass even when the engine did not accept")
	}
	if jobPassed(rows[1]) {
		t.Error("a completed engine accept whose candidate failed the test must not pass")
	}
	if got := passSource(rows[0]); got != "code_test" {
		t.Errorf("pass source = %q, want code_test", got)
	}

	// Legacy rows (no recorded test outcome and no label version) keep the
	// historical engine-accept label so old results.json files still re-render.
	legacy := jobMetrics{Archetype: "code", Tier: "H", Arm: "full", State: "completed", Winner: "new"}
	if !jobPassed(legacy) {
		t.Error("legacy code row should fall back to engine accept")
	}
	if got := passSource(legacy); got != "engine_accept" {
		t.Errorf("legacy pass source = %q, want engine_accept", got)
	}

	// A post-ADR-0092 row with no tests_ran event is test-missing, not an
	// engine accept. It must not silently pass because the engine completed.
	missing := jobMetrics{Archetype: "code", Tier: "H", Arm: "full",
		State: "completed", Winner: "new", LabelVersion: benchLabelVersion}
	if jobPassed(missing) {
		t.Error("code row with no recorded test must not pass on engine acceptance")
	}
	if got := passSource(missing); got != "code_test_missing" {
		t.Errorf("missing-test pass source = %q, want code_test_missing", got)
	}
}

// TestCodeLabelTable pins the label accounting: test pass, engine accept,
// test-pass-not-accepted, test-missing, and legacy rows are separate counts.
func TestCodeLabelTable(t *testing.T) {
	pass, fail := true, false
	rows := codeLabelTable([]jobMetrics{
		{ModelLabel: "m1", Arm: "full", Archetype: "code", State: "failed", CodeTestPassed: &pass, LabelVersion: benchLabelVersion},
		{ModelLabel: "m1", Arm: "full", Archetype: "code", State: "completed", Winner: "new", CodeTestPassed: &fail, LabelVersion: benchLabelVersion},
		{ModelLabel: "m1", Arm: "full", Archetype: "code", State: "completed", Winner: "new", LabelVersion: benchLabelVersion},
		{ModelLabel: "m1", Arm: "full", Archetype: "code", State: "completed", Winner: "new"},
	})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Rows != 4 || r.TestPass != 1 || r.EngineAccept != 1 ||
		r.TestPassNotAccepted != 1 || r.TestMissing != 1 || r.Legacy != 1 || r.LegacyEngineAccepted != 1 {
		t.Errorf("label accounting = %+v", r)
	}
}

// TestReportSurfacesCodeTestMismatch pins the diagnostic that made the 0/6
// X-code claim ambiguous: a recorded test pass that the engine did not accept
// is shown, not hidden behind the engine's state.
func TestReportSurfacesCodeTestMismatch(t *testing.T) {
	pass := true
	out := renderReport([]jobMetrics{{
		GoalName: "x-code-synthetic", Archetype: "code", Tier: "X", Arm: "full",
		ModelLabel: "m1", State: "failed", Winner: "none", CodeTestPassed: &pass,
	}})
	if !strings.Contains(out, "## Code test label vs engine accept") {
		t.Error("report is missing the code test-vs-accept table")
	}
	if !strings.Contains(out, "x-code-synthetic") {
		t.Error("report did not surface the test-pass-but-not-accepted row")
	}
}

func TestArmTable(t *testing.T) {
	metrics := []jobMetrics{
		mk("a", "single", 1, 0.4, 0.5, 0, "new", 100),
		mk("a", "dialectical", 1, 0.6, 0.7, 0.8, "new", 300),
		mk("a", "dialectical", 2, 0.8, 0.7, 0.6, "new", 300),
		mk("b", "dialectical", 1, 0.5, 0.6, 0.7, "previous", 250),
	}
	byKey := map[string]armRow{}
	for _, r := range armTable(metrics) {
		byKey[r.Goal+"/"+r.Arm] = r
	}
	if r := byKey["a/dialectical"]; r.N != 2 || !almost(r.Score, 0.7) || r.Tokens != 300 {
		t.Errorf("a/dialectical = %+v, want n=2 mean=0.7 tok=300", r)
	}
	if r := byKey["a/single"]; !almost(r.Score, 0.4) {
		t.Errorf("a/single = %+v, want 0.4", r)
	}
	if r := byKey["b/dialectical"]; r.Model != "m1" || r.Family != "fam" {
		t.Errorf("model/family = %q/%q, want m1/fam", r.Model, r.Family)
	}
}

func TestCalibrationPairs(t *testing.T) {
	metrics := []jobMetrics{
		mk("a", "dialectical", 1, 0.8, 0.7, 0, "new", 1),
		mk("a", "single", 1, 0.4, 0.5, 0, "new", 1),         // excluded (single arm)
		{GoalName: "b", Arm: "dialectical", State: "error"}, // excluded
	}
	pairs := calibrationPairs(metrics)
	if len(pairs) != 2 {
		t.Fatalf("pairs = %d, want 2 (all arms; errors excluded)", len(pairs))
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

func TestDiversityTable(t *testing.T) {
	metrics := []jobMetrics{
		mk("a", "dialectical", 1, 1, 1, 0.8, "new", 1),
		mk("a", "dialectical", 2, 1, 1, 0.4, "new", 1),
		mk("b", "single", 1, 1, 1, 0, "new", 1),
	}
	byArm := map[string]divRow{}
	for _, r := range diversityTable(metrics) {
		byArm[r.Arm] = r
	}
	if !almost(byArm["dialectical"].Mean, 0.6) {
		t.Errorf("dialectical mean = %v, want 0.6", byArm["dialectical"].Mean)
	}
	if byArm["single"].Mean != 0 {
		t.Errorf("single mean = %v, want 0", byArm["single"].Mean)
	}
}

func TestRenderReport(t *testing.T) {
	out := renderReport([]jobMetrics{
		mk("a", "single", 1, 0.4, 0.5, 0, "new", 100),
		mk("a", "dialectical", 1, 0.6, 0.7, 0.5, "new", 300),
	})
	for _, want := range []string{"Scores", "model", "Corpus", "T-b", "T-c", "candidate diversity", "code"} {
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
