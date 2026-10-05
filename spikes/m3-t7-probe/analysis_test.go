package main

import (
	"math"
	"testing"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestJaccardDistance(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		{"identical", "a b c", "a b c", 0},
		{"disjoint", "a b", "c d", 1},
		{"half overlap", "a b c", "a b d", 0.5},
		{"both empty", "", "   ", 0},
		{"one empty", "a b", "", 1},
		{"duplicates collapse to a set", "a a b", "a b", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jaccardDistance(c.a, c.b); !almost(got, c.want) {
				t.Errorf("jaccardDistance(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestAvgPairwiseJaccard(t *testing.T) {
	if got := avgPairwiseJaccard(nil); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
	if got := avgPairwiseJaccard([]candidate{{Text: "a"}}); got != 0 {
		t.Errorf("single candidate = %v, want 0", got)
	}
	identical := []candidate{{Text: "x y z"}, {Text: "z y x"}, {Text: "x y z"}}
	if got := avgPairwiseJaccard(identical); !almost(got, 0) {
		t.Errorf("identical candidates = %v, want 0", got)
	}
	disjoint := []candidate{{Text: "a"}, {Text: "b"}}
	if got := avgPairwiseJaccard(disjoint); !almost(got, 1) {
		t.Errorf("disjoint pair = %v, want 1", got)
	}
}

func TestConfidenceBin(t *testing.T) {
	cases := []struct {
		c    float64
		want int
	}{
		{0.5, 0}, {0.59, 0}, {0.6, 1}, {0.7, 2}, {0.8, 3},
		{0.99, 4}, {1.0, 4}, {0.49, -1}, {0.0, -1}, {1.1, -1},
		{math.NaN(), -1},
	}
	for _, c := range cases {
		if got := confidenceBin(c.c); got != c.want {
			t.Errorf("confidenceBin(%v) = %d, want %d", c.c, got, c.want)
		}
	}
}

func TestCalibrationTable(t *testing.T) {
	rows := calibrationTable([]confScore{
		{0.55, 0.5}, {0.55, 0.7}, // bin 0, mean 0.6
		{0.75, 0.9}, // bin 2, mean 0.9
		{0.3, 1.0},  // dropped (< 0.5)
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (empty bins omitted)", len(rows))
	}
	if rows[0].Label != "[0.5,0.6)" || rows[0].N != 2 || !almost(rows[0].MeanObserved, 0.6) {
		t.Errorf("row 0 = %+v, want label [0.5,0.6) n=2 mean=0.6", rows[0])
	}
	if rows[1].Label != "[0.7,0.8)" || rows[1].N != 1 || !almost(rows[1].MeanObserved, 0.9) {
		t.Errorf("row 1 = %+v, want label [0.7,0.8) n=1 mean=0.9", rows[1])
	}
	if got := calibrationTable(nil); got != nil {
		t.Errorf("empty input = %+v, want nil", got)
	}
}

func TestStabilityClass(t *testing.T) {
	cases := []struct {
		name    string
		winners []string
		want    string
	}{
		{"three same", []string{"new", "new", "new"}, "3-same"},
		{"two one", []string{"new", "new", "previous"}, "2-1"},
		{"three way", []string{"new", "previous", "none"}, "3-way"},
		{"too few", []string{"new"}, "insufficient"},
		{"all same n=2", []string{"new", "new"}, "all-same"},
		{"majority n=4", []string{"new", "new", "previous", "none"}, "majority"},
		{"all distinct n=4", []string{"a", "b", "c", "d"}, "all-distinct"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stabilityClass(c.winners); got != c.want {
				t.Errorf("stabilityClass(%v) = %q, want %q", c.winners, got, c.want)
			}
		})
	}
}

func TestMean(t *testing.T) {
	if got := mean(nil); got != 0 {
		t.Errorf("mean(nil) = %v, want 0", got)
	}
	if got := mean([]float64{1, 2, 3, 4}); !almost(got, 2.5) {
		t.Errorf("mean = %v, want 2.5", got)
	}
}
