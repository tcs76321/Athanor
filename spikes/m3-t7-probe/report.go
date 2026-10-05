package main

import (
	"fmt"
	"sort"
	"strings"
)

// headlineRow pairs the single-shot and dialectical outcomes for one
// goal. Delta is dialectical minus single (positive = the loop helped).
type headlineRow struct {
	Goal              string
	Archetype         string
	SingleScore       float64
	DialecticalScore  float64
	Delta             float64
	SingleTokens      float64
	DialecticalTokens float64
}

// armMeans returns the mean score and mean token cost for one (goal,
// arm), ignoring errored rows. A missing arm yields zeroes.
func armMeans(metrics []jobMetrics, goalName, armName string) (score, tokens float64) {
	var scores, toks []float64
	for _, m := range metrics {
		if m.GoalName == goalName && m.Arm == armName && m.State != "error" {
			scores = append(scores, m.Score)
			toks = append(toks, float64(m.TokenCost))
		}
	}
	return mean(scores), mean(toks)
}

// headlineTable builds the per-goal paired comparison. Goals are sorted
// by name for a stable report.
func headlineTable(metrics []jobMetrics) []headlineRow {
	archetype := make(map[string]string)
	var order []string
	for _, m := range metrics {
		if _, ok := archetype[m.GoalName]; !ok {
			archetype[m.GoalName] = m.Archetype
			order = append(order, m.GoalName)
		}
	}
	sort.Strings(order)
	out := make([]headlineRow, 0, len(order))
	for _, g := range order {
		s, st := armMeans(metrics, g, "single")
		d, dt := armMeans(metrics, g, "dialectical")
		out = append(out, headlineRow{
			Goal: g, Archetype: archetype[g],
			SingleScore: s, DialecticalScore: d, Delta: d - s,
			SingleTokens: st, DialecticalTokens: dt,
		})
	}
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

// stabilityTally classifies each goal's dialectical winners across its
// runs and tallies the class frequency — the T-c input.
func stabilityTally(metrics []jobMetrics) map[string]int {
	byGoal := make(map[string][]string)
	for _, m := range metrics {
		if m.Arm == "dialectical" && m.State != "error" {
			byGoal[m.GoalName] = append(byGoal[m.GoalName], m.Winner)
		}
	}
	tally := make(map[string]int)
	for _, winners := range byGoal {
		tally[stabilityClass(winners)]++
	}
	return tally
}

// meanDiversity averages the candidate-set diversity across an arm's
// completed runs. The single arm has one candidate, so its diversity is
// zero by construction.
func meanDiversity(metrics []jobMetrics, armName string) float64 {
	var ds []float64
	for _, m := range metrics {
		if m.Arm == armName && m.State != "error" {
			ds = append(ds, m.Diversity)
		}
	}
	return mean(ds)
}

// renderReport renders the markdown findings skeleton over all collected
// metrics. It fills the tables mechanically; interpretation and the ADR
// trigger stay with the human writing the findings.
func renderReport(metrics []jobMetrics) string {
	var b strings.Builder
	b.WriteString("# M3-T7 results\n\n")
	fmt.Fprintf(&b, "Collected %d job rows.\n\n", len(metrics))

	b.WriteString("## Headline — dialectical vs single-shot\n\n")
	b.WriteString("| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range headlineTable(metrics) {
		fmt.Fprintf(&b, "| %s | %s | %.2f | %.2f | %+.2f | %.0f | %.0f |\n",
			r.Goal, r.Archetype, r.SingleScore, r.DialecticalScore, r.Delta, r.SingleTokens, r.DialecticalTokens)
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

	b.WriteString("\n## T-a — candidate diversity\n\n")
	fmt.Fprintf(&b, "Mean pairwise Jaccard distance (dialectical): %.3f\n", meanDiversity(metrics, "dialectical"))
	fmt.Fprintf(&b, "Mean pairwise Jaccard distance (single): %.3f\n", meanDiversity(metrics, "single"))
	b.WriteString("\n## Environment / caveats\n\n")
	b.WriteString("- Fill in the model digests, Ollama version, and any confounds from the daemon logs.\n")
	b.WriteString("- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.\n")
	return b.String()
}
