package main

import (
	"fmt"
	"sort"
	"strings"
)

// armRow is one (model, goal, arm) cell: the mean score and token cost across
// the arm's runs. It is arm-agnostic so the soak's `full` / ablation arms read
// the same as the default dialectical-vs-single pair.
type armRow struct {
	Model     string
	Family    string
	Goal      string
	Archetype string
	Arm       string
	N         int
	Score     float64
	Tokens    float64
}

// armTable groups the metrics by (model, goal, arm). Errored rows are ignored.
func armTable(metrics []jobMetrics) []armRow {
	type key struct{ model, goal, arm string }
	agg := map[key]*armRow{}
	var order []key
	for _, m := range metrics {
		if m.State == "error" {
			continue
		}
		k := key{m.ModelLabel, m.GoalName, m.Arm}
		if _, ok := agg[k]; !ok {
			agg[k] = &armRow{
				Model: m.ModelLabel, Family: m.Family, Goal: m.GoalName,
				Archetype: m.Archetype, Arm: m.Arm,
			}
			order = append(order, k)
		}
		r := agg[k]
		r.N++
		r.Score += m.Score
		r.Tokens += float64(m.TokenCost)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].model != order[j].model {
			return order[i].model < order[j].model
		}
		if order[i].goal != order[j].goal {
			return order[i].goal < order[j].goal
		}
		return order[i].arm < order[j].arm
	})
	out := make([]armRow, 0, len(order))
	for _, k := range order {
		r := agg[k]
		r.Score /= float64(r.N)
		r.Tokens /= float64(r.N)
		out = append(out, *r)
	}
	return out
}

// divRow is one (model, arm) diversity cell.
type divRow struct {
	Model string
	Arm   string
	Mean  float64
	N     int
}

// diversityTable groups the T-a candidate diversity by (model, arm).
func diversityTable(metrics []jobMetrics) []divRow {
	type key struct{ model, arm string }
	agg := map[key]*divRow{}
	var order []key
	for _, m := range metrics {
		if m.State == "error" {
			continue
		}
		k := key{m.ModelLabel, m.Arm}
		if _, ok := agg[k]; !ok {
			agg[k] = &divRow{Model: m.ModelLabel, Arm: m.Arm}
			order = append(order, k)
		}
		agg[k].Mean += m.Diversity
		agg[k].N++
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].model != order[j].model {
			return order[i].model < order[j].model
		}
		return order[i].arm < order[j].arm
	})
	out := make([]divRow, 0, len(order))
	for _, k := range order {
		r := agg[k]
		if r.N > 0 {
			r.Mean /= float64(r.N)
		}
		out = append(out, *r)
	}
	return out
}

// calibrationPairs returns (comparison confidence, observed score) pairs across
// all arms — the T-b reliability-diagram input.
func calibrationPairs(metrics []jobMetrics) []confScore {
	var out []confScore
	for _, m := range metrics {
		if m.State != "error" {
			out = append(out, confScore{Confidence: m.Confidence, Observed: m.Score})
		}
	}
	return out
}

// stabilityTally classifies each (model, goal, arm)'s winners across its runs
// and tallies the class frequency — the T-c input.
func stabilityTally(metrics []jobMetrics) map[string]int {
	byKey := map[string][]string{}
	for _, m := range metrics {
		if m.State == "error" {
			continue
		}
		k := m.ModelLabel + "\x00" + m.GoalName + "\x00" + m.Arm
		byKey[k] = append(byKey[k], m.Winner)
	}
	tally := make(map[string]int)
	for _, winners := range byKey {
		tally[stabilityClass(winners)]++
	}
	return tally
}

// verificationSummary counts completed code accepts and how many were decided
// without the LLM judge (F4-T3) — the deterministic-accept fraction.
func verificationSummary(metrics []jobMetrics) (codeAccepts, deterministicAccepts int) {
	for _, m := range metrics {
		if m.Archetype != "code" || m.Winner != "new" || m.State != "completed" {
			continue
		}
		codeAccepts++
		if m.JudgeCalled != nil && !*m.JudgeCalled {
			deterministicAccepts++
		}
	}
	return codeAccepts, deterministicAccepts
}

// tierPassRow is the deterministic pass count for one (tier, arm) cell.
type tierPassRow struct {
	Tier string
	Arm  string
	N    int
	Pass int
}

// jobPassed is the deterministic acceptance signal for one job: the task's
// non-code checks when it has them, else the engine's accept (a completed job
// whose winning artifact is new). M8-T21.
func jobPassed(m jobMetrics) bool {
	if m.CheckPass != nil {
		return *m.CheckPass
	}
	return m.State == "completed" && m.Winner == "new"
}

// corpusSummary aggregates the deterministic pass rate by (tier, arm) — the
// corpus read that answers "does the loop help, and where". M8-T21.
func corpusSummary(metrics []jobMetrics) []tierPassRow {
	type key struct{ tier, arm string }
	agg := map[key]*tierPassRow{}
	var order []key
	for _, m := range metrics {
		k := key{m.Tier, m.Arm}
		if _, ok := agg[k]; !ok {
			agg[k] = &tierPassRow{Tier: m.Tier, Arm: m.Arm}
			order = append(order, k)
		}
		r := agg[k]
		r.N++
		if jobPassed(m) {
			r.Pass++
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].tier != order[j].tier {
			return order[i].tier < order[j].tier
		}
		return order[i].arm < order[j].arm
	})
	out := make([]tierPassRow, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	return out
}

// heldOutRow is the deterministic pass count for one (held_out, arm) cell.
type heldOutRow struct {
	HeldOut bool
	Arm     string
	N       int
	Pass    int
}

// heldOutSummary aggregates the bench corpus pass rate by held-out vs tuning
// and arm. The locked M3-T7 set (Bench=false) is excluded: it is a different
// instrument and must not inflate the tuning slice.
func heldOutSummary(metrics []jobMetrics) []heldOutRow {
	type key struct {
		held bool
		arm  string
	}
	agg := map[key]*heldOutRow{}
	var order []key
	for _, m := range metrics {
		if !m.Bench {
			continue
		}
		k := key{m.HeldOut, m.Arm}
		if _, ok := agg[k]; !ok {
			agg[k] = &heldOutRow{HeldOut: m.HeldOut, Arm: m.Arm}
			order = append(order, k)
		}
		r := agg[k]
		r.N++
		if jobPassed(m) {
			r.Pass++
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].held != order[j].held {
			return !order[i].held
		}
		return order[i].arm < order[j].arm
	})
	out := make([]heldOutRow, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	return out
}

// renderReport renders the markdown findings skeleton over all collected
// metrics. It fills the tables mechanically; interpretation and the ADR
// trigger stay with the human writing the findings.
func renderReport(metrics []jobMetrics) string {
	var b strings.Builder
	b.WriteString("# M3-T7 results\n\n")
	fmt.Fprintf(&b, "Collected %d job rows.\n\n", len(metrics))

	if lb := renderLeaderboard(metrics); lb != "" {
		b.WriteString(lb)
		b.WriteString("\n")
	}

	b.WriteString("## Scores — per (model, goal, arm)\n\n")
	b.WriteString("| model | family | goal | archetype | arm | n | mean score | mean tok |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range armTable(metrics) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %.2f | %.0f |\n",
			r.Model, r.Family, r.Goal, r.Archetype, r.Arm, r.N, r.Score, r.Tokens)
	}

	b.WriteString("\n## Corpus — deterministic pass rate (tier × arm)\n\n")
	b.WriteString("| tier | arm | n | pass | rate |\n|---|---|---|---|---|\n")
	for _, r := range corpusSummary(metrics) {
		rate := 0.0
		if r.N > 0 {
			rate = float64(r.Pass) / float64(r.N) * 100
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %.0f%% |\n", r.Tier, r.Arm, r.N, r.Pass, rate)
	}

	b.WriteString("\n## Corpus — held-out vs tuning\n\n")
	b.WriteString("The held-out slice is never tuned on. A gain that appears only in the tuning\n")
	b.WriteString("slice is not evidence of generalization.\n\n")
	b.WriteString("| slice | arm | n | pass | rate |\n|---|---|---|---|---|\n")
	for _, r := range heldOutSummary(metrics) {
		label := "tuning"
		if r.HeldOut {
			label = "held-out"
		}
		rate := 0.0
		if r.N > 0 {
			rate = float64(r.Pass) / float64(r.N) * 100
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %.0f%% |\n", label, r.Arm, r.N, r.Pass, rate)
	}

	b.WriteString("\n## T-b — judge-confidence calibration (all arms)\n\n")
	b.WriteString("| confidence bin | n | mean observed score |\n|---|---|---|\n")
	for _, row := range calibrationTable(calibrationPairs(metrics)) {
		fmt.Fprintf(&b, "| %s | %d | %.2f |\n", row.Label, row.N, row.MeanObserved)
	}

	b.WriteString("\n## T-c — verdict stability (per arm)\n\n")
	tally := stabilityTally(metrics)
	b.WriteString("| class | goals |\n|---|---|\n")
	for _, class := range []string{"3-same", "2-1", "3-way", "all-same", "majority", "all-distinct", "insufficient"} {
		if n, ok := tally[class]; ok {
			fmt.Fprintf(&b, "| %s | %d |\n", class, n)
		}
	}

	b.WriteString("\n## T-a — candidate diversity (per model × arm)\n\n")
	b.WriteString("| model | arm | mean diversity |\n|---|---|---|\n")
	for _, r := range diversityTable(metrics) {
		fmt.Fprintf(&b, "| %s | %s | %.3f |\n", r.Model, r.Arm, r.Mean)
	}

	b.WriteString(renderTelemetryTables(metrics))

	b.WriteString("\n## F4 — verification decisions\n\n")
	accepts, det := verificationSummary(metrics)
	if accepts == 0 {
		b.WriteString("No completed code accepts in this run.\n")
	} else {
		fmt.Fprintf(&b, "Code accepts: %d; decided without the LLM judge: %d (%.0f%%).\n",
			accepts, det, float64(det)/float64(accepts)*100)
	}
	judged := 0
	mismatched := 0
	for _, m := range metrics {
		if m.JudgeCalled != nil && *m.JudgeCalled {
			judged++
		}
		if m.CrossFamilyOK != nil && !*m.CrossFamilyOK {
			mismatched++
		}
	}
	fmt.Fprintf(&b, "Comparisons that called the LLM judge: %d.\n", judged)
	if mismatched > 0 {
		fmt.Fprintf(&b, "WARNING: %d comparison(s) had cross_family_ok=false — the judge shared the generator's family.\n", mismatched)
	}

	b.WriteString("\n## Decision diagnostics — external check passed but not accepted\n\n")
	b.WriteString("These rows passed the benchmark's own deterministic check yet the engine did not\n")
	b.WriteString("complete with a new winner. The guard columns show whether the §19.3 guard\n")
	b.WriteString("downgraded a supported `new`, or the judge itself declined.\n\n")
	b.WriteString("| goal | model | state | winner | pre | post | thr | prev | final pass | final conf | reason |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	anomalies := 0
	for _, m := range metrics {
		if m.CheckPass == nil || !*m.CheckPass || (m.State == "completed" && m.Winner == "new") {
			continue
		}
		anomalies++
		finalPass := ""
		if m.FinalRecordPassed != nil {
			finalPass = fmt.Sprintf("%t", *m.FinalRecordPassed)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %.2f | %t | %s | %.2f | %s |\n",
			m.GoalName, m.ModelLabel, m.State, m.Winner, m.GuardPreWinner, m.GuardPostWinner,
			m.GuardThreshold, m.HasPrevious, finalPass, m.FinalRecordConfidence, m.DecideReason)
	}
	if anomalies == 0 {
		b.WriteString("| — | — | — | — | — | — | — | — | — | — | none |\n")
	}

	b.WriteString("\n## Environment / caveats\n\n")
	b.WriteString("- Fill in the model digests, Ollama version, and any confounds from the daemon logs.\n")
	b.WriteString("- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.\n")
	return b.String()
}

// guidanceTierRow is one tier's deterministic pass rate across all arms.
type guidanceTierRow struct {
	Tier string
	N    int
	Pass int
}

// passRate is pass/n, guarding the n == 0 case.
func passRate(pass, n int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(pass) / float64(n)
}

// tierPassRate aggregates the corpus deterministic pass rate over arms into one
// row per tier (ascending).
func tierPassRate(metrics []jobMetrics) []guidanceTierRow {
	agg := map[string]*guidanceTierRow{}
	var order []string
	for _, m := range metrics {
		r, ok := agg[m.Tier]
		if !ok {
			r = &guidanceTierRow{Tier: m.Tier}
			agg[m.Tier] = r
			order = append(order, m.Tier)
		}
		r.N++
		if jobPassed(m) {
			r.Pass++
		}
	}
	sort.Strings(order)
	out := make([]guidanceTierRow, 0, len(order))
	for _, t := range order {
		out = append(out, *agg[t])
	}
	return out
}

// renderGuidanceDelta renders the M8-T30 guidance A/B read: the deterministic
// (grounded) pass rate per tier for a baseline run (guidance OFF) and the
// current run (guidance ON), with the delta. It reports the observed delta and
// deliberately makes no significance claim (ADR-0066/0067): a short corpus run
// has no power to prove an improvement, only to show the loop moved a grounded
// metric.
func renderGuidanceDelta(baseline, current []jobMetrics) string {
	base := map[string]guidanceTierRow{}
	for _, r := range tierPassRate(baseline) {
		base[r.Tier] = r
	}
	cur := tierPassRate(current)

	var b strings.Builder
	b.WriteString("# Guidance A/B — deterministic pass rate (M8-T30)\n\n")
	b.WriteString("Baseline = guidance OFF (no active technique insights); current = guidance ON.\n")
	b.WriteString("Per-tier rate over all arms. A positive delta is the advisory technique\n")
	b.WriteString("loop's observed effect; small-n runs are not a significance claim.\n\n")
	b.WriteString("| tier | baseline n | baseline rate | current n | current rate | delta |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, c := range cur {
		br := base[c.Tier]
		baseRate := passRate(br.Pass, br.N)
		curRate := passRate(c.Pass, c.N)
		fmt.Fprintf(&b, "| %s | %d | %.0f%% | %d | %.0f%% | %+.0f pp |\n",
			c.Tier, br.N, baseRate*100, c.N, curRate*100, (curRate-baseRate)*100)
	}
	return b.String()
}
