package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// fakeContextProvider is the MCE seam stub (M5-T5.5); cmd/ holds the real
// one over mce.ChunkStore.
type fakeContextProvider struct {
	chunk    prompt.ChunkText
	hasChunk bool
	index    []prompt.IndexLine
	err      error
}

func (f fakeContextProvider) ActiveChunk(context.Context, string) (prompt.ChunkText, bool, error) {
	return f.chunk, f.hasChunk, f.err
}

func (f fakeContextProvider) DormantIndex(context.Context, string) ([]prompt.IndexLine, error) {
	return f.index, f.err
}

// withSeams rebuilds the env's engine with an eviction seam and/or a
// context provider. The engine is stateless before a job runs, so swapping
// the whole value keeps the harness intact (the withEvictor precedent).
func (e *testEnv) withSeams(t *testing.T, ev Evictor, provider ContextProvider) {
	t.Helper()
	registry, err := llm.NewRegistry(e.cfg.Personas)
	if err != nil {
		t.Fatal(err)
	}
	e.eng = New(e.cfg, e.db, e.jobs, e.projects, e.artifacts, evaluation.NewRepo(e.db),
		llm.NewClient(e.cfg.Inference.OllamaURL, nil), registry, e.freezer,
		power.NewPowerManager(nil), e.runner, ev, provider, nil)
}

// TestPromptTiersFeedActiveChunkAndDormantIndex proves the M5-T5 read
// path end-to-end: the MCE's active chunk reaches §11.2 §7 and the
// Dormant Index reaches §11.2 §10, with the §10.4 swap invitation naming
// the tool that loads them.
func TestPromptTiersFeedActiveChunkAndDormantIndex(t *testing.T) {
	provider := fakeContextProvider{
		chunk: prompt.ChunkText{
			ID: "c1", RelPath: "widget.go", LineStart: 1, LineEnd: 4,
			Kind: "ast", Text: "package widget\nfunc W() {}\n",
		},
		hasChunk: true,
		index: []prompt.IndexLine{
			{ChunkID: "c2", Summary: "the gadget", RelPath: "gadget.go", LineStart: 1, LineEnd: 9, Kind: "ast"},
		},
	}
	e := newEnv(t)
	e.withSeams(t, nil, provider)

	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", nil)
	e.eng.Run(context.Background(), jobID)

	served := e.ollama.lastPromptText()
	for _, fragment := range []string{
		"ACTIVE CODE CHUNK", "chunk_id: c1", "package widget",
		"DORMANT INDEX", "chunk_id=c2", "the gadget", "context_swap(target_chunk_id)",
	} {
		if !strings.Contains(served, fragment) {
			t.Errorf("served prompt missing %q", fragment)
		}
	}
}

// TestNilProviderKeepsPromptsPreT5 is the regression guard: with no MCE
// seam wired, tiers 3 and 6 are absent and nothing else changes.
func TestNilProviderKeepsPromptsPreT5(t *testing.T) {
	e := newEnv(t)
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", nil)
	e.eng.Run(context.Background(), jobID)

	served := e.ollama.lastPromptText()
	if strings.Contains(served, "ACTIVE CODE CHUNK") || strings.Contains(served, "DORMANT INDEX") {
		t.Error("nil provider must leave tiers 3 and 6 empty")
	}
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("job state = %s, want completed", j.State)
	}
}

// TestSuppressionPersistsAcrossCallsAndHonorsRestart proves ADR-0023 §5:
// an assembly eviction is written to `system_state`, the next call honours
// it (no re-rendering the evicted tier, no thrashing), and a fresh engine
// over the same database loads the same set.
func TestSuppressionPersistsAcrossCallsAndHonorsRestart(t *testing.T) {
	// Size the window from a baseline so the diverging call must evict
	// its tier-7 instructions.
	pinned, _ := measuredPhasePrompt(t, llm.PhasePlanning)
	largest, _ := measuredPrompt(t, nil)
	if largest <= pinned {
		t.Skipf("baseline adds no evictable content (pinned=%d, largest=%d)", pinned, largest)
	}
	window := (pinned + largest) / 2 * 100 / 95

	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.ContextEngine.SimpleFloor = 128
		shrinkAllWindows(cfg, window)
	})
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", nil)
	job, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	p, task, err := e.contextsForTest(t, jobID)
	if err != nil {
		t.Fatal(err)
	}

	// First call: the phase instructions (tier 7) are too large, so the
	// assembly evicts them and records the suppression.
	seed := strings.Repeat("think hard about the goal. ", 200)
	if _, err := e.eng.call(context.Background(), job, p, task,
		llm.PhaseDiverging, llm.RoleMain, seed, nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	raw := suppressedRow(t, e, jobID)
	if raw == "" {
		t.Fatal("no suppression row after an assembly eviction")
	}
	var stored []prompt.Tier
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("decode suppression row %q: %v", raw, err)
	}
	if len(stored) != 1 || stored[0] != prompt.TierInstructions {
		t.Fatalf("stored suppression = %v, want [instructions(7)]", stored)
	}

	// Second call: the suppression is honoured — the instruction text is
	// gone, the pinned content is not.
	seed2 := strings.Repeat("keep thinking about the goal. ", 200)
	if _, err := e.eng.call(context.Background(), job, p, task,
		llm.PhaseDiverging, llm.RoleMain, seed2, nil); err != nil {
		t.Fatalf("second call: %v", err)
	}
	served := e.ollama.lastPromptText()
	if strings.Contains(served, "keep thinking about the goal.") {
		t.Error("second call re-rendered the suppressed tier")
	}
	if !strings.Contains(served, "CONTAINMENT RULES") {
		t.Error("second call lost pinned tier-1 content")
	}

	// A fresh engine over the same store sees the same suppression (a
	// restart must not resurrect evicted tiers, §23.6).
	e.withSeams(t, nil, nil)
	loaded := e.eng.loadSuppressedTiers(context.Background(), jobID)
	if len(loaded) != 1 || loaded[0] != prompt.TierInstructions {
		t.Fatalf("reloaded suppression = %v, want [instructions(7)]", loaded)
	}

	// Terminal state clears it.
	e.eng.clearSuppressedTiers(context.Background(), jobID)
	if got := suppressedRow(t, e, jobID); got != "" {
		t.Errorf("suppression row survived clear: %q", got)
	}
}

// TestCeilingForReusesTheCriticalThreshold pins ADR-0023 §3: the assembly
// budget is the §10.4 critical threshold applied to the persona window,
// with a non-positive input meaning "unbounded" rather than "evict all".
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

// TestToolManifestResolvesEnvelope pins ADR-0023 §8: the manifest mirrors
// what the server would allow, the task override wins over the daemon
// default, and an unparseable envelope yields no manifest (claiming fewer
// tools is the safe way to be wrong).
func TestToolManifestResolvesEnvelope(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	eng := &Engine{cfg: cfg}

	if got := eng.toolManifest(project.Task{ID: "t1"}); len(got) != 0 {
		t.Errorf("empty envelope manifest = %v, want none", got)
	}
	cfg.JobPod.DefaultTools = []string{"execute_code"}
	if got := eng.toolManifest(project.Task{ID: "t1"}); len(got) != 1 || got[0] != "execute_code" {
		t.Errorf("default manifest = %v, want [execute_code]", got)
	}
	got := eng.toolManifest(project.Task{ID: "t1", AllowedTools: []string{"lint", "context_swap"}})
	if len(got) != 2 || got[0] != "context_swap" || got[1] != "lint" {
		t.Errorf("task override manifest = %v, want [context_swap lint] (sorted)", got)
	}
	if got := eng.toolManifest(project.Task{ID: "t1", AllowedTools: []string{"not_a_tool"}}); got != nil {
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

// TestLadderEvictorIsOneStep pins the §10.4 seam: one tier per call, and
// an empty result (→ pause) when nothing evictable remains.
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
		t.Fatalf("Evict = %+v, want instructions/50", got)
	}
	got, err = ev.Evict(context.Background(), "job", weights, []prompt.Tier{prompt.TierInstructions})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tiers) != 1 || got.Tiers[0] != prompt.TierDormantIndex {
		t.Fatalf("Evict after suppressing 7 = %+v, want dormant_index", got)
	}
	got, err = ev.Evict(context.Background(), "job",
		map[prompt.Tier]int{prompt.TierStaticSystem: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tiers) != 0 || got.FreedTokens != 0 {
		t.Fatalf("Evict with only pinned tiers = %+v, want empty (pause path)", got)
	}
}
