package policy

import (
	"fmt"
	"testing"
)

func TestExploreMonotonicInNovelty(t *testing.T) {
	keys := make([]string, 0, 4000)
	for i := 0; i < 4000; i++ {
		keys = append(keys, fmt.Sprintf("job-%05d", i))
	}
	rate := func(novelty float64) float64 {
		n := 0
		for _, k := range keys {
			if Explore(k, novelty) {
				n++
			}
		}
		return float64(n) / float64(len(keys))
	}
	if got := rate(1); got != 1.0 {
		t.Errorf("fully-novel explore rate = %v, want 1.0", got)
	}
	familiar := rate(0)
	if familiar < 0.05 || familiar > 0.16 {
		t.Errorf("familiar explore rate = %v, want ~%.2f", familiar, ExplorationEpsilon)
	}
	mid := rate(0.5)
	if mid < familiar || mid > 1.0 {
		t.Errorf("explore rate not monotonic in novelty: familiar=%v mid=%v", familiar, mid)
	}
}

func TestExploreDeterministicAndBounded(t *testing.T) {
	for _, k := range []string{"a", "job-x", "", "0189ab"} {
		want := Explore(k, 0.3)
		for i := 0; i < 5; i++ {
			if Explore(k, 0.3) != want {
				t.Fatalf("Explore(%q) is not deterministic", k)
			}
		}
	}
	// Out-of-range novelty clamps rather than panicking.
	if !Explore("k", 5) {
		t.Error("novelty > 1 should clamp to always-explore")
	}
	if Explore("k", -5) != Explore("k", 0) {
		t.Error("novelty < 0 should clamp to the familiar case")
	}
}
