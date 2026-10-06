package main

import (
	"fmt"
	"sort"
	"strings"
)

// headlineRow pairs the single-shot and dialectical outcomes for one
// (model, goal). Delta is dialectical minus single (positive = the loop
// helped).
type headlineRow struct {
	Model             string
	Family            string
	Goal              string
	Archetype         string
	SingleScore       float64
	DialecticalScore  float64
	Delta             float64
	SingleTokens      float64
	DialecticalTokens float64
}

// armMeans returns the mean score and mean token cost for one
// (model, goal, arm), ignoring errored rows. A missing arm yields zeroes.
func armMeans(metrics []jobMetrics, model, goalName, armName string) (score, tokens float64) {
	var scores, toks []float64
	for _, m := range metrics {
		if m.ModelLabel == model && m.GoalName == goalName && m.Arm == armName && m.State != "error" {
			scores = append(scores, m.Score)
			toks = append(toks, float64(m.TokenCost))
		}
	}
	return mean(scores), mean(toks)
}

// headlineTable builds the per-(model, goal) paired comparison. Rows are
// sorted by model then goal for a stable, self-describing report.
func headlineTable(metrics []jobMetrics) []headlineRow {
	type key struct{ model, goal string }
	seen := map[key]bool{}
	arch := map[key]string{}
	family := map[string]string{}
	var keys []key
	for _, m := range metrics {
		k := key{m.ModelLabel, m.GoalName}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
		arch[k] = m.Archetype
		if m.Family != "" {
			family[m.ModelLabel] = m.Family
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		return keys[i].goal < keys[j].goal
	})
	out := make([]headlineRow, 0, len(keys))
	for _, k := range keys {
		s, st := armMeans(metrics, k.model, k.goal, "single")
		d, dt := armMeans(metrics, k.model, k.goal, "dialectical")
		out = append(out, headlineRow{
			Model: k.model, Family: family[k.model], Goal: k.goal, Archetype: arch[k],
			SingleScore: s, DialecticalScore: d, Delta: d - s,
			SingleTokens: st, DialecticalTokens: dt,
		})
	}
	return out
}

// modelLabels returns the distinct model labels, sorted.
func modelLabels(metrics []jobMetrics) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range metrics {
		if m.ModelLabel == "" || seen[m.ModelLabel] {
			continue
		}
		seen[m.ModelLabel] = true
		out = append(out, m.ModelLabel)
	}
	sort.Strings(out)
	return out
}

// calibrationPairs returns (comparison confidence, observed score) pairs
// from the dialectical arm — the T-b reliability-diagram input.
func calibrationPairs(metrics []jobMetrics) []confScore {
	var out []confScore
	for _, m := range metrics {
		if m.Arm == "dialectical" && m.State != "error" {
			out = append(out, confScore{Confidence: m.Confidence, Observed: m.Score})
		}
	}
	return out
}

// stabilityTally classifies each (model, goal)'s dialectical winners across
// its runs and tallies the class frequency — the T-c input.
func stabilityTally(metrics []jobMetrics) map[string]int {
	byKey := make(map[string][]string)
	for _, m := range metrics {
		if m.Arm == "dialectical" && m.State != "error" {
			byKey[m.ModelLabel+"\x00"+m.GoalName] = append(byKey[m.ModelLabel+"\x00"+m.GoalName], m.Winner)
		}
	}
	tally := make(map[string]int)
	for _, winners := range byKey {
		tally[stabilityClass(winners)]++
	}
	return tally
}

// meanDiversity averages the candidate-set diversity across one model's
// completed runs of an arm. The single arm has one candidate, so its
// diversity is zero by construction.
func meanDiversity(metrics []jobMetrics, model, armName string) float64 {
	var ds []float64
	for _, m := range metrics {
		if m.ModelLabel == model && m.Arm == armName && m.State != "error" {
			ds = append(ds, m.Diversity)
		}
	}
	return mean(ds)
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

// renderReport renders the markdown findings skeleton over all collected
// metrics. It fills the tables mechanically; interpretation and the ADR
// trigger stay with the human writing the findings.
func renderReport(metrics []jobMetrics) string {
	var b strings.Builder
	b.WriteString("# M3-T7 results\n\n")
	fmt.Fprintf(&b, "Collected %d job rows.\n\n", len(metrics))

	b.WriteString("## Headline — dialectical vs single-shot (per model)\n\n")
	b.WriteString("| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range headlineTable(metrics) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %.2f | %.2f | %+.2f | %.0f | %.0f |\n",
			r.Model, r.Family, r.Goal, r.Archetype, r.SingleScore, r.DialecticalScore, r.Delta, r.SingleTokens, r.DialecticalTokens)
	}

	b.WriteString("\n## T-b — judge-confidence calibration (dialectical arm)\n\n")
	b.WriteString("| confidence bin | n | mean observed score |\n|---|---|---|\n")
	for _, row := range calibrationTable(calibrationPairs(metrics)) {
		fmt.Fprintf(&b, "| %s | %d | %.2f |\n", row.Label, row.N, row.MeanObserved)
	}

	b.WriteString("\n## T-c — verdict stability (dialectical arm)\n\n")
	tally := stabilityTally(metrics)
	b.WriteString("| class | goals |\n|---|---|\n")
	for _, class := range []string{"3-same", "2-1", "3-way", "all-same", "majority", "all-distinct", "insufficient"} {
		if n, ok := tally[class]; ok {
			fmt.Fprintf(&b, "| %s | %d |\n", class, n)
		}
	}

	b.WriteString("\n## T-a — candidate diversity (per model)\n\n")
	b.WriteString("| model | dialectical | single |\n|---|---|---|\n")
	for _, label := range modelLabels(metrics) {
		fmt.Fprintf(&b, "| %s | %.3f | %.3f |\n",
			label, meanDiversity(metrics, label, "dialectical"), meanDiversity(metrics, label, "single"))
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
