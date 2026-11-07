package onboard

import "testing"

func TestQualificationPlanIsBalanced(t *testing.T) {
	l := DefaultLadder()
	core := QualificationCore(l)
	extra := QualificationExtra(l)
	seen := map[string]bool{}
	caps := map[string]bool{}
	for _, c := range core {
		key := c.Kind + "|" + c.ID
		if seen[key] {
			t.Errorf("duplicate core case %s", key)
		}
		seen[key] = true
		caps[c.Capability] = true
	}
	for _, want := range []string{"summarize", "security", "agentry", "instruction", "grounding"} {
		if !caps[want] {
			t.Errorf("core has no %s case", want)
		}
	}
	for _, c := range extra {
		if seen[c.Kind+"|"+c.ID] {
			t.Errorf("extra repeats core case %s|%s", c.Kind, c.ID)
		}
	}
	blocks := QualificationBlocks(l, 7)
	total := 0
	for _, b := range blocks {
		total += len(b)
	}
	if total != len(extra) {
		t.Errorf("blocks cover %d extra cases, want %d", total, len(extra))
	}
}

func TestBalancedCaseKeysUsesIntersection(t *testing.T) {
	results := map[string]bool{
		"generator|a|g1": true, "judge|a|j1": true,
		"generator|b|g1": true,
	}
	got := BalancedCaseKeys(results, []string{"a", "b"})
	if !got["generator|g1"] || got["judge|j1"] {
		t.Fatalf("balanced = %v, want only generator|g1", got)
	}
}
