package main

import (
	"strings"
	"testing"
)

// TestLeaderboardTable pins the ADR-0073 §3 leaderboard reduction: grounded
// pass rate, cost per passing job, and wall-time percentiles per model.
func TestLeaderboardTable(t *testing.T) {
	yes, no := true, false
	metrics := []jobMetrics{
		{ModelLabel: "m1", Family: "fam", State: "completed", Winner: "new", CheckPass: &yes, TokenCost: 100, WallMS: 1000},
		{ModelLabel: "m1", Family: "fam", State: "completed", Winner: "new", CheckPass: &no, TokenCost: 300, WallMS: 3000},
		{ModelLabel: "m2", Family: "other", State: "failed", Winner: "none", TokenCost: 10, WallMS: 500},
	}
	rows := leaderboardTable(metrics)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	byModel := map[string]leaderRow{}
	for _, r := range rows {
		byModel[r.Model] = r
	}
	m1 := byModel["m1"]
	if m1.Jobs != 2 || m1.Pass != 1 {
		t.Errorf("m1 = %d jobs / %d pass, want 2/1", m1.Jobs, m1.Pass)
	}
	if !almost(m1.Rate, 0.5) {
		t.Errorf("m1 rate = %v, want 0.5", m1.Rate)
	}
	if !almost(m1.TokensPerPass, 400) {
		t.Errorf("m1 tokens/pass = %v, want 400", m1.TokensPerPass)
	}
	if m1.P50WallMS != 1000 || m1.P95WallMS != 3000 {
		t.Errorf("m1 wall p50/p95 = %d/%d, want 1000/3000", m1.P50WallMS, m1.P95WallMS)
	}
	if m2 := byModel["m2"]; m2.Pass != 0 || m2.TokensPerPass != 0 {
		t.Errorf("m2 = %+v, want 0 pass and no tokens/pass", m2)
	}
}

func TestRenderLeaderboard(t *testing.T) {
	yes := true
	out := renderLeaderboard([]jobMetrics{
		{ModelLabel: "m1", Family: "fam", State: "completed", Winner: "new", CheckPass: &yes, TokenCost: 100, WallMS: 1000},
	})
	for _, want := range []string{"Model leaderboard", "m1", "tok/pass", "p95 wall"} {
		if !strings.Contains(out, want) {
			t.Errorf("leaderboard missing %q\n%s", want, out)
		}
	}
	if renderLeaderboard(nil) != "" {
		t.Error("empty metrics must render an empty leaderboard")
	}
}

// TestPercentileInt pins the nearest-rank helper including the single-sample
// and clamping edges.
func TestPercentileInt(t *testing.T) {
	cases := []struct {
		xs   []int
		q    float64
		want int
	}{
		{nil, 0.5, 0},
		{[]int{7}, 0.95, 7},
		{[]int{1, 2, 3, 4}, 0.5, 2},
		{[]int{1, 2, 3, 4}, 0.95, 4},
		{[]int{4, 1, 3, 2}, 0.5, 2},
	}
	for _, c := range cases {
		if got := percentileInt(c.xs, c.q); got != c.want {
			t.Errorf("percentileInt(%v, %v) = %d, want %d", c.xs, c.q, got, c.want)
		}
	}
}
