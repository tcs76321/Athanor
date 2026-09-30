package prompt

import (
	"reflect"
	"testing"
)

// weights7 is the standard fixture: pinned tiers cost 100 each, evictable
// tiers 50 each (total 500). Each ladder row below picks a ceiling that
// forces a specific number of evictions.
func weights7() map[Tier]int {
	return map[Tier]int{
		TierStaticSystem: 100, TierTaskCriteria: 100, TierWorkingSet: 100,
		TierCorrections: 50, TierEpisodic: 50, TierDormantIndex: 50, TierInstructions: 50,
	}
}

// TestApplyLadderEvictionOrder is the M5-T5 acceptance criterion
// (ROADMAP §M5-T5, §10.5): overflow evicts tiers 7→6→5→4 only, stopping
// at the first total that fits, and tiers 1–3 are never evicted.
func TestApplyLadderEvictionOrder(t *testing.T) {
	tests := []struct {
		name      string
		ceiling   int
		suppress  []Tier
		weights   map[Tier]int
		wantEvict []Tier
		wantDrop  int
		wantKept  int
		wantFits  bool
	}{
		{
			name: "at ceiling: no eviction", ceiling: 500,
			wantEvict: nil, wantDrop: 0, wantKept: 500, wantFits: true,
		},
		{
			name: "7 alone suffices", ceiling: 450,
			wantEvict: []Tier{TierInstructions}, wantDrop: 50, wantKept: 450, wantFits: true,
		},
		{
			name: "7 then 6", ceiling: 400,
			wantEvict: []Tier{TierInstructions, TierDormantIndex},
			wantDrop:  100, wantKept: 400, wantFits: true,
		},
		{
			name: "7 then 6 then 5", ceiling: 350,
			wantEvict: []Tier{TierInstructions, TierDormantIndex, TierEpisodic},
			wantDrop:  150, wantKept: 350, wantFits: true,
		},
		{
			name: "7 6 5 4 — pinned tiers survive at the limit", ceiling: 300,
			wantEvict: []Tier{TierInstructions, TierDormantIndex, TierEpisodic, TierCorrections},
			wantDrop:  200, wantKept: 300, wantFits: true,
		},
		{
			name:    "pinned tiers alone exceed the ceiling: fits=false, nothing more to evict",
			ceiling: 250, wantEvict: []Tier{TierInstructions, TierDormantIndex, TierEpisodic, TierCorrections},
			wantDrop: 200, wantKept: 300, wantFits: false,
		},
		{
			name: "avoidable tier is skipped when absent", ceiling: 350,
			weights: map[Tier]int{
				TierStaticSystem: 100, TierTaskCriteria: 100, TierWorkingSet: 100,
				TierCorrections: 50, TierEpisodic: 50, TierDormantIndex: 0, TierInstructions: 100,
			},
			wantEvict: []Tier{TierInstructions, TierEpisodic},
			wantDrop:  150, wantKept: 350, wantFits: true,
		},
		{
			name: "unbounded ceiling evicts nothing", ceiling: 0,
			wantEvict: nil, wantDrop: 0, wantKept: 500, wantFits: true,
		},
		{
			name: "already-suppressed tiers are excluded and not re-reported", ceiling: 400,
			suppress:  []Tier{TierInstructions},
			wantEvict: []Tier{TierDormantIndex}, wantDrop: 50, wantKept: 400, wantFits: true,
		},
		{
			name: "suppression makes a run fit with no new eviction", ceiling: 450,
			suppress:  []Tier{TierDormantIndex},
			wantEvict: nil, wantDrop: 0, wantKept: 450, wantFits: true,
		},
		{
			name: "pinned tiers in corrupted suppression are ignored", ceiling: 500,
			suppress:  []Tier{TierStaticSystem, TierTaskCriteria, TierWorkingSet},
			wantEvict: nil, wantDrop: 0, wantKept: 500, wantFits: true,
		},
		{
			name: "corrupt pinned suppression cannot manufacture headroom", ceiling: 250,
			suppress:  []Tier{TierStaticSystem, TierTaskCriteria, TierWorkingSet},
			wantEvict: []Tier{TierInstructions, TierDormantIndex, TierEpisodic, TierCorrections},
			wantDrop:  200, wantKept: 300, wantFits: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := tt.weights
			if w == nil {
				w = weights7()
			}
			got := applyLadder(w, tt.ceiling, tt.suppress)
			if len(got.Evicted) != len(tt.wantEvict) {
				t.Fatalf("Evicted = %v, want %v", got.Evicted, tt.wantEvict)
			}
			for i := range got.Evicted {
				if got.Evicted[i] != tt.wantEvict[i] {
					t.Fatalf("Evicted = %v, want %v (ladder order)", got.Evicted, tt.wantEvict)
				}
			}
			if got.DroppedTokens != tt.wantDrop {
				t.Errorf("DroppedTokens = %d, want %d", got.DroppedTokens, tt.wantDrop)
			}
			if got.KeptTokens != tt.wantKept {
				t.Errorf("KeptTokens = %d, want %d", got.KeptTokens, tt.wantKept)
			}
			if got.Fits != tt.wantFits {
				t.Errorf("Fits = %v, want %v", got.Fits, tt.wantFits)
			}
		})
	}
}

// TestApplyLadderNeverEvictsPinned pins the invariant structurally: no
// ceiling and no suppression list can put a tier 1–3 into the report.
func TestApplyLadderNeverEvictsPinned(t *testing.T) {
	suppress := []Tier{
		TierStaticSystem, TierTaskCriteria, TierWorkingSet,
		TierCorrections, TierEpisodic, TierDormantIndex, TierInstructions,
	}
	for ceiling := -100; ceiling <= 700; ceiling += 25 {
		got := applyLadder(weights7(), ceiling, suppress)
		for _, e := range got.Evicted {
			if e.Pinned() {
				t.Fatalf("ceiling %d: evicted pinned tier %v", ceiling, e)
			}
		}
	}
}

// TestApplyLadderReportsSuppressedUnion pins the report's Suppressed set:
// the input's suppression plus this pass's evictions, ascending, so a
// renderer never recomputes it (M5-T5.3).
func TestApplyLadderReportsSuppressedUnion(t *testing.T) {
	got := applyLadder(weights7(), 350, []Tier{TierCorrections})
	// Pre-suppressed corrections (50) leave 450; the ladder drops
	// instructions (400) then dormant_index (350, fits) — episodic is
	// untouched.
	want := []Tier{TierCorrections, TierDormantIndex, TierInstructions}
	if !reflect.DeepEqual(got.Suppressed, want) {
		t.Fatalf("Suppressed = %v, want %v", got.Suppressed, want)
	}
	if got.Suppresses(TierCorrections) != true || got.Suppresses(TierStaticSystem) != false {
		t.Errorf("Suppresses: got %v/%v, want true/false",
			got.Suppresses(TierCorrections), got.Suppresses(TierStaticSystem))
	}
	// Pinned tiers can never appear in the union, whatever the input says.
	corrupt := applyLadder(weights7(), 1, []Tier{TierStaticSystem, TierTaskCriteria, TierWorkingSet})
	for _, s := range corrupt.Suppressed {
		if s.Pinned() {
			t.Fatalf("corrupt suppression leaked pinned tier %v into the report", s)
		}
	}
}

// TestEvictNextIsOneLadderStep pins §10.4's force-eviction as a single
// bottom-up step: tier 7 first, skipping absent and suppressed tiers, and
// reporting false (→ pause) when only the pinned tiers remain.
func TestEvictNextIsOneLadderStep(t *testing.T) {
	w := weights7()
	if tier, tok, ok := EvictNext(w, nil); !ok || tier != TierInstructions || tok != 50 {
		t.Fatalf("EvictNext = (%v, %d, %v), want (instructions, 50, true)", tier, tok, ok)
	}
	if tier, tok, ok := EvictNext(w, []Tier{TierInstructions}); !ok || tier != TierDormantIndex || tok != 50 {
		t.Fatalf("EvictNext after suppressing 7 = (%v, %d, %v), want (dormant_index, 50, true)", tier, tok, ok)
	}
	// Absent tiers are skipped rather than returned with weight 0.
	sparse := map[Tier]int{TierStaticSystem: 100, TierInstructions: 0, TierCorrections: 20}
	if tier, tok, ok := EvictNext(sparse, nil); !ok || tier != TierCorrections || tok != 20 {
		t.Fatalf("EvictNext(sparse) = (%v, %d, %v), want (corrections, 20, true)", tier, tok, ok)
	}
	// Only pinned content left → nothing evictable, which is the pause path.
	pinnedOnly := map[Tier]int{TierStaticSystem: 100, TierTaskCriteria: 100, TierWorkingSet: 100}
	if tier, tok, ok := EvictNext(pinnedOnly, nil); ok {
		t.Fatalf("EvictNext(pinned only) = (%v, %d, true), want ok=false", tier, tok)
	}
	// A corrupt weights map naming a pinned tier cannot produce one.
	corrupt := map[Tier]int{TierStaticSystem: 100, TierInstructions: 7}
	for i := 0; i < 4; i++ {
		tier, _, ok := EvictNext(corrupt, nil)
		if ok && tier.Pinned() {
			t.Fatalf("EvictNext returned pinned tier %v", tier)
		}
		if !ok {
			break
		}
		corrupt[tier] = 0
	}
}

// TestTierClassification pins the tier metadata the ladder and audit rows
// depend on (same posture as the engine's DecideWinner contract tests).
func TestTierClassification(t *testing.T) {
	wantNames := map[Tier]string{
		TierStaticSystem: "static_system", TierTaskCriteria: "task_criteria",
		TierWorkingSet: "working_set", TierCorrections: "corrections",
		TierEpisodic: "episodic", TierDormantIndex: "dormant_index",
		TierInstructions: "instructions",
	}
	for tier, name := range wantNames {
		if got := tier.String(); got != name {
			t.Errorf("Tier(%d).String() = %q, want %q", tier, got, name)
		}
		if tier.Pinned() == tier.Evictable() {
			t.Errorf("Tier(%d): Pinned and Evictable must be inverses", tier)
		}
	}
	for _, tier := range []Tier{TierStaticSystem, TierTaskCriteria, TierWorkingSet} {
		if !tier.Pinned() {
			t.Errorf("Tier(%d) must be pinned", tier)
		}
	}
	for _, tier := range []Tier{TierCorrections, TierEpisodic, TierDormantIndex, TierInstructions} {
		if !tier.Evictable() {
			t.Errorf("Tier(%d) must be evictable", tier)
		}
	}
	if !reflect.DeepEqual(ladderOrder, []Tier{TierInstructions, TierDormantIndex, TierEpisodic, TierCorrections}) {
		t.Errorf("ladderOrder = %v, want 7→6→5→4", ladderOrder)
	}
}
