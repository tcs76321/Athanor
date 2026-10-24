package main

import (
	"strings"
	"testing"
)

func TestRenderGuidanceDelta(t *testing.T) {
	mk := func(tier string, pass bool) jobMetrics {
		p := pass
		return jobMetrics{Tier: tier, Arm: "full", State: "completed", Winner: "new", CheckPass: &p}
	}
	// baseline (guidance off): M 0/2, H 1/2.
	baseline := []jobMetrics{mk("M", false), mk("M", false), mk("H", true), mk("H", false)}
	// current (guidance on): M 2/2, H 1/2.
	current := []jobMetrics{mk("M", true), mk("M", true), mk("H", true), mk("H", false)}

	out := renderGuidanceDelta(baseline, current)
	for _, want := range []string{"| M |", "| H |", "+100 pp"} {
		if !strings.Contains(out, want) {
			t.Errorf("guidance delta missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "significant") {
		t.Errorf("delta must not claim significance:\n%s", out)
	}
}
