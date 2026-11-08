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

func (f fakeContextProvider) ActiveChunk(context.Context, ContextQuery) (prompt.ChunkText, bool, error) {
	return f.chunk, f.hasChunk, f.err
}

func (f fakeContextProvider) DormantIndex(context.Context, ContextQuery) ([]prompt.IndexLine, error) {
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
		"DORMANT INDEX", "chunk_id=c2", "the gadget", "may load a dormant chunk",
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
		llm.PhaseDiverging, llm.RoleMain, seed, nil, 0); err != nil {
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
		llm.PhaseDiverging, llm.RoleMain, seed2, nil, 0); err != nil {
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
	loaded := e.eng.state.EvictedTiers(context.Background(), jobID)
	if len(loaded) != 1 || loaded[0] != prompt.TierInstructions {
		t.Fatalf("reloaded suppression = %v, want [instructions(7)]", loaded)
	}

	// Terminal state clears it.
	if err := e.eng.state.SetEvictedTiers(context.Background(), jobID, nil); err != nil {
		t.Fatal(err)
	}
	if got := suppressedRow(t, e, jobID); got != "" {
		t.Errorf("suppression row survived clear: %q", got)
	}
}
