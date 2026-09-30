// M5-T5.6 adapter tests: the MCE→engine boundary (ADR-0019 inversion).
// internal/mce and internal/engine never import each other; this file
// proves the manufactured bridge maps storage records into the prompt
// package's tier payloads faithfully, and that serve.go's composition is
// sound.
package main

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/mce/division"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// migratedChunkStore opens a migrated temp database behind a ChunkStore
// (the openMigrated + Migrate pair the MCE boot tests already use).
func migratedChunkStore(t *testing.T) *mce.ChunkStore {
	t.Helper()
	st := openMigrated(t)
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return mce.NewChunkStore(st)
}

// TestServeCompositionProvidesMCEContext mirrors serve.go's M5-T5.6
// composition: the MCE runtime built at boot, wired into the engine as the
// §10.4 ladder seam plus a provider gated on the swapping flag. It is the
// structural proof that the pieces fit before the daemon ever starts.
func TestServeCompositionProvidesMCEContext(t *testing.T) {
	st := openMigrated(t)
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	rt, err := startMCE(st, testRegistry(t), llm.NewClient("http://127.0.0.1:1", nil),
		config.ContextEngine{EnableLosslessSwapping: boolPtr(true)})
	if err != nil {
		t.Fatalf("startMCE: %v", err)
	}
	// The compile-time `var _ engine.ContextProvider` assertion in
	// context_provider.go proves the adapter type; here we prove both are
	// usable through the interfaces serve.go passes to engine.New.
	var provider engine.ContextProvider = newContextProvider(rt.Store, rt.LosslessSwapping)
	evictor := engine.NewLadderEvictor()
	// A seam with nothing evictable reports an empty eviction, which is
	// the gate's pause path — not an error and not a bogus free count.
	got, err := evictor.Evict(context.Background(), "job-1", nil, nil)
	if err != nil || len(got.Tiers) != 0 || got.FreedTokens != 0 {
		t.Fatalf("Evict(no tiers) = (%+v, %v), want empty eviction", got, err)
	}
	// The gate is the runtime's own flag: enabled here, so the provider is
	// live (an empty store simply yields no working set).
	if _, ok, err := provider.ActiveChunk(context.Background(), "job-1"); err != nil || ok {
		t.Fatalf("ActiveChunk on an empty store = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestContextProviderMapsActiveChunkAndIndex proves the adapter's mapping:
// the active chunk's bytes and metadata reach prompt.ChunkText verbatim,
// and the Dormant Index rows reach prompt.IndexLine with their summaries.
func TestContextProviderMapsActiveChunkAndIndex(t *testing.T) {
	cs := migratedChunkStore(t)
	ctx := context.Background()
	src := []byte("package demo\n\nfunc A() {}\n\nfunc B() {}\n")
	chunks := division.New(division.Options{}).Divide("demo.go", "go", src)
	if len(chunks) < 2 {
		t.Fatalf("division produced %d chunks, need >= 2", len(chunks))
	}
	if _, err := cs.PutSource(ctx, mce.SourceRef{RelPath: "demo.go", JobID: "job-1"}, chunks); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Swap(ctx, "job-1", chunks[1].ID); err != nil {
		t.Fatalf("swap to second chunk: %v", err)
	}

	p := newContextProvider(cs, true)
	chunk, ok, err := p.ActiveChunk(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("ActiveChunk = (%v, %v), want a chunk", ok, err)
	}
	if chunk.ID != chunks[1].ID {
		t.Errorf("active chunk id = %s, want %s (the swapped one)", chunk.ID, chunks[1].ID)
	}
	if chunk.RelPath != "demo.go" || chunk.Kind != string(chunks[1].Kind) {
		t.Errorf("chunk metadata = %+v, want demo.go/%s", chunk, chunks[1].Kind)
	}
	if chunk.Text != string(chunks[1].Content) {
		t.Errorf("chunk text = %q, want the stored bytes %q", chunk.Text, chunks[1].Content)
	}

	index, err := p.DormantIndex(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != len(chunks) {
		t.Fatalf("index rows = %d, want %d", len(index), len(chunks))
	}
	for _, line := range index {
		if line.ChunkID == "" || line.RelPath != "demo.go" {
			t.Errorf("index line = %+v, want a demo.go chunk", line)
		}
		if line.Summary != "" {
			t.Errorf("summary = %q, want empty while summary_status is pending", line.Summary)
		}
	}

	// A job with no chunks sees an empty working set, not an error.
	if _, ok, err := p.ActiveChunk(ctx, "job-other"); err != nil || ok {
		t.Errorf("ActiveChunk(other) = (%v, %v), want (false, nil)", ok, err)
	}
	if idx, err := p.DormantIndex(ctx, "job-other"); err != nil || len(idx) != 0 {
		t.Errorf("DormantIndex(other) = (%d, %v), want (0, nil)", len(idx), err)
	}
}

// TestContextProviderRespectsLosslessSwappingGate proves the adapter
// honours the same flag as ingestion and the context_swap route: with
// swapping disabled the working set is unavailable, so tiers 3 and 6 stay
// empty rather than exposing context the operator switched off.
func TestContextProviderRespectsLosslessSwappingGate(t *testing.T) {
	cs := migratedChunkStore(t)
	ctx := context.Background()
	chunks := division.New(division.Options{}).Divide("demo.go", "go", []byte("package demo\n"))
	if _, err := cs.PutSource(ctx, mce.SourceRef{RelPath: "demo.go", JobID: "job-1"}, chunks); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Swap(ctx, "job-1", chunks[0].ID); err != nil {
		t.Fatal(err)
	}

	disabled := newContextProvider(cs, false)
	if _, ok, err := disabled.ActiveChunk(ctx, "job-1"); err != nil || ok {
		t.Errorf("disabled ActiveChunk = (%v, %v), want (false, nil)", ok, err)
	}
	if idx, err := disabled.DormantIndex(ctx, "job-1"); err != nil || len(idx) != 0 {
		t.Errorf("disabled DormantIndex = (%d, %v), want (0, nil)", len(idx), err)
	}

	// A nil store (MCE not built) must behave the same way, not panic.
	empty := newContextProvider(nil, true)
	if _, ok, err := empty.ActiveChunk(ctx, "job-1"); err != nil || ok {
		t.Errorf("nil-store ActiveChunk = (%v, %v), want (false, nil)", ok, err)
	}
	if idx, err := empty.DormantIndex(ctx, "job-1"); err != nil || len(idx) != 0 {
		t.Errorf("nil-store DormantIndex = (%d, %v), want (0, nil)", len(idx), err)
	}
}
