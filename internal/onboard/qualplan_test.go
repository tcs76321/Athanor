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

func TestQualificationDepthsGrowAndNest(t *testing.T) {
	defaultLadder := DefaultLadder()
	deepLadder := DeepLadder()
	minimal := QualificationMinimal(defaultLadder)
	balanced := AllCaseRefs(defaultLadder)
	complete := AllCaseRefs(deepLadder)

	t.Logf("minimal=%d balanced=%d complete=%d cases", len(minimal), len(balanced), len(complete))
	if len(minimal) < 2*len(qualificationCapabilityOrder)-2 {
		t.Errorf("minimal = %d cases, want at least two per capability", len(minimal))
	}
	if len(balanced) < len(minimal)*3/2 {
		t.Errorf("balanced = %d, minimal = %d; want balanced at least 1.5x minimal", len(balanced), len(minimal))
	}
	if len(complete) <= len(balanced) {
		t.Errorf("complete = %d, balanced = %d; want complete to add the deep cases", len(complete), len(balanced))
	}
	inBalanced := map[string]bool{}
	for _, ref := range balanced {
		inBalanced[ref.Kind+"|"+ref.ID] = true
	}
	for _, ref := range minimal {
		if !inBalanced[ref.Kind+"|"+ref.ID] {
			t.Errorf("minimal case %s|%s is not in the balanced plan", ref.Kind, ref.ID)
		}
	}
	inComplete := map[string]bool{}
	for _, ref := range complete {
		inComplete[ref.Kind+"|"+ref.ID] = true
	}
	for _, ref := range balanced {
		if !inComplete[ref.Kind+"|"+ref.ID] {
			t.Errorf("balanced case %s|%s is not in the complete plan", ref.Kind, ref.ID)
		}
	}
}

func TestQualificationMinimalCoversCapabilities(t *testing.T) {
	l := DefaultLadder()
	minimal := QualificationMinimal(l)
	counts := map[string]int{}
	for _, ref := range minimal {
		counts[ref.Capability]++
	}
	for capability, cases := range qualificationCapabilityCounts(l) {
		want := 2
		if cases < want {
			want = cases
		}
		if got := counts[capability]; got < want {
			t.Errorf("minimal has %d %s cases, want %d", got, capability, want)
		}
	}
	if !hasCapability(minimal, "judgment") {
		// Judgment has no case with that capability; the fallback judge pair
		// is still required, so at least one judge must be present.
		hasJudge := false
		for _, ref := range minimal {
			if ref.Kind == "judge" {
				hasJudge = true
			}
		}
		if !hasJudge {
			t.Error("minimal has no judge pair")
		}
	}
}

func qualificationCapabilityCounts(l Ladder) map[string]int {
	counts := map[string]int{}
	for _, ref := range AllCaseRefs(l) {
		counts[ref.Capability]++
	}
	return counts
}

func TestBalancedCaseKeysUsesIntersection(t *testing.T) {
	results := map[string]bool{
		"generator|a|g1": true, "judge|a|j1": true,
		"generator|b|g1": false, // completed, but failed
	}
	got := BalancedCaseKeys(results, []string{"a", "b"})
	if !got["generator|g1"] {
		t.Fatalf("balanced = %v, want generator|g1 (completion, not pass)", got)
	}
	if got["judge|j1"] {
		t.Fatalf("balanced = %v, judge|j1 is not completed by every model", got)
	}
}
