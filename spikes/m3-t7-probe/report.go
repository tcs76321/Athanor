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

// renderReport renders the markdown findings skeleton over all collected
// metrics. It fills the tables mechanically; interpretation and the ADR
// trigger stay with the human writing the findings.
func renderReport(metrics []jobMetrics) string {
	var b strings.Builder
	b.WriteString("# M3-T7 results\n\n")
	fmt.Fprintf(&b, "Collected %d job rows.\n\n", len(metrics))

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

	b.WriteString("\n## Environment / caveats\n\n")
	b.WriteString("- Fill in the model digests, Ollama version, and any confounds from the daemon logs.\n")
	b.WriteString("- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.\n")
	return b.String()
}
