// Model leaderboard (ADR-0073 §3). The per-(model, goal, arm) score table does
// not answer "which model should I run"; this reduces a results tree — from
// `soak` or `run` — into one row per model: how often it passed, what it cost,
// and how long it took.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// leaderRow is one model's aggregate across the whole results tree.
type leaderRow struct {
	Model         string
	Family        string
	Jobs          int
	Pass          int
	Rate          float64
	MeanScore     float64
	MeanTokens    float64
	TokensPerPass float64
	P50WallMS     int
	P95WallMS     int
	MeanCalls     float64
}

// leaderboardTable aggregates every job by model. "Pass" is jobPassed (the
// deterministic signal, M8-T21), so the ranking is grounded, not judge-based.
// Tokens-per-pass divides total tokens by passing jobs, so a model that fails
// cheaply still ranks poorly.
func leaderboardTable(metrics []jobMetrics) []leaderRow {
	type agg struct {
		family           string
		jobs, pass       int
		score, tokens    float64
		wall             []int
		calls, withCalls int
	}
	byModel := map[string]*agg{}
	var order []string
	for _, m := range metrics {
		if m.ModelLabel == "" {
			continue
		}
		a := byModel[m.ModelLabel]
		if a == nil {
			a = &agg{}
			byModel[m.ModelLabel] = a
			order = append(order, m.ModelLabel)
		}
		a.jobs++
		a.score += m.Score
		a.tokens += float64(m.TokenCost)
		a.wall = append(a.wall, m.WallMS)
		if m.Family != "" {
			a.family = m.Family
		}
		if jobPassed(m) {
			a.pass++
		}
		if m.Telemetry != nil {
			a.calls += m.Telemetry.Calls
			a.withCalls++
		}
	}
	sort.Strings(order)
	out := make([]leaderRow, 0, len(order))
	for _, model := range order {
		a := byModel[model]
		r := leaderRow{Model: model, Family: a.family, Jobs: a.jobs, Pass: a.pass}
		if a.jobs > 0 {
			r.Rate = float64(a.pass) / float64(a.jobs)
			r.MeanScore = a.score / float64(a.jobs)
			r.MeanTokens = a.tokens / float64(a.jobs)
		}
		if a.pass > 0 {
			r.TokensPerPass = a.tokens / float64(a.pass)
		}
		r.P50WallMS = percentileInt(a.wall, 0.50)
		r.P95WallMS = percentileInt(a.wall, 0.95)
		if a.withCalls > 0 {
			r.MeanCalls = float64(a.calls) / float64(a.withCalls)
		}
		out = append(out, r)
	}
	return out
}

// percentileInt returns the nearest-rank percentile (0 < q <= 1) of xs, or 0
// for an empty slice.
func percentileInt(xs []int, q float64) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	idx := int(math.Ceil(q*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

// renderLeaderboard renders the per-model leaderboard section for the report.
func renderLeaderboard(metrics []jobMetrics) string {
	rows := leaderboardTable(metrics)
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Model leaderboard (ADR-0073)\n\n")
	b.WriteString("Grounded pass rate (deterministic checks / engine accept), cost, and wall-time percentiles per model.\n\n")
	b.WriteString("| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %.0f%% | %.2f | %.0f | %.0f | %.1f | %.1f | %.1f |\n",
			r.Model, r.Family, r.Jobs, r.Pass, r.Rate*100, r.MeanScore, r.MeanTokens,
			r.TokensPerPass, float64(r.P50WallMS)/1000, float64(r.P95WallMS)/1000, r.MeanCalls)
	}
	return b.String()
}
