package contextasm

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// TestCeilingForReusesTheCriticalThreshold pins ADR-0023 §3: the assembly
// budget is the §10.4 critical threshold applied to the persona window, with a
// non-positive input meaning "unbounded" rather than "evict all".
func TestCeilingForReusesTheCriticalThreshold(t *testing.T) {
	ce := config.ContextEngine{KVCacheWarningThresh: 0.85, KVCacheCriticalThresh: 0.95}
	want := int(ce.KVCacheCriticalThresh * float64(32768))
	if got := ceilingFor(ce, 32768); got != want {
		t.Errorf("ceilingFor(32768) = %d, want %d", got, want)
	}
	for _, tc := range []struct {
		name string
		ce   config.ContextEngine
		w    int
	}{
		{"zero window", ce, 0},
		{"negative window", ce, -1},
		{"zero threshold", config.ContextEngine{}, 32768},
	} {
		if got := ceilingFor(tc.ce, tc.w); got != 0 {
			t.Errorf("%s: ceilingFor = %d, want 0 (unbounded)", tc.name, got)
		}
	}
}

// TestToolManifestResolvesEnvelope pins ADR-0023 §8: the manifest mirrors what
// the server would allow, the task override wins over the daemon default, and
// an unparseable envelope yields no manifest.
func TestToolManifestResolvesEnvelope(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	a := &Assembler{cfg: cfg}

	if got := a.toolManifest(project.Task{ID: "t1"}); len(got) != 0 {
		t.Errorf("empty envelope manifest = %v, want none", got)
	}
	cfg.JobPod.DefaultTools = []string{"execute_code"}
	if got := a.toolManifest(project.Task{ID: "t1"}); len(got) != 1 || got[0] != "execute_code" {
		t.Errorf("default manifest = %v, want [execute_code]", got)
	}
	got := a.toolManifest(project.Task{ID: "t1", AllowedTools: []string{"lint", "context_swap"}})
	if len(got) != 2 || got[0] != "context_swap" || got[1] != "lint" {
		t.Errorf("task override manifest = %v, want [context_swap lint] (sorted)", got)
	}
	if got := a.toolManifest(project.Task{ID: "t1", AllowedTools: []string{"not_a_tool"}}); got != nil {
		t.Errorf("unparseable envelope manifest = %v, want nil", got)
	}
}

// TestNormalizeSuppressedDropsPinnedAndSorts pins the state hygiene the
// suppression row depends on.
func TestNormalizeSuppressedDropsPinnedAndSorts(t *testing.T) {
	got := normalizeSuppressed([]prompt.Tier{
		prompt.TierInstructions, prompt.TierStaticSystem, prompt.TierDormantIndex,
		prompt.TierInstructions, prompt.TierWorkingSet,
	})
	want := []prompt.Tier{prompt.TierDormantIndex, prompt.TierInstructions}
	if len(got) != len(want) {
		t.Fatalf("normalizeSuppressed = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeSuppressed = %v, want %v", got, want)
		}
	}
	if normalizeSuppressed(nil) != nil {
		t.Error("normalizeSuppressed(nil) must be nil")
	}
	if normalizeSuppressed([]prompt.Tier{prompt.TierStaticSystem}) != nil {
		t.Error("pinned-only input must normalize to nil")
	}
}

// TestLadderEvictorIsOneStep pins the §10.4 seam: one tier per call, and an
// empty result (→ pause) when nothing evictable remains.
func TestLadderEvictorIsOneStep(t *testing.T) {
	ev := NewLadderEvictor()
	weights := map[prompt.Tier]int{
		prompt.TierStaticSystem: 100, prompt.TierInstructions: 50, prompt.TierDormantIndex: 20,
	}
	got, err := ev.Evict(context.Background(), "job", weights, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tiers) != 1 || got.Tiers[0] != prompt.TierInstructions || got.FreedTokens != 50 {
		t.Errorf("eviction = %+v, want [instructions] 50", got)
	}
	again, err := ev.Evict(context.Background(), "job", weights, got.Tiers)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Tiers) != 1 || again.Tiers[0] != prompt.TierDormantIndex {
		t.Errorf("second eviction = %+v, want [dormant_index]", again)
	}
	none, err := ev.Evict(context.Background(), "job", map[prompt.Tier]int{
		prompt.TierStaticSystem: 100,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Tiers) != 0 {
		t.Errorf("pinned-only eviction = %+v, want empty", none)
	}
}
