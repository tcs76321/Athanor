// M5-T5.6 / F5 adapter tests: the MCE→engine boundary (ADR-0019 inversion).
// internal/mce and internal/engine never import each other; this file proves
// the manufactured bridge maps storage records into the prompt package's tier
// payloads faithfully, that the project Dormant Index union works (ADR-0059),
// and that serve.go's composition is sound.
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

// TestServeCompositionProvidesMCEContext mirrors serve.go's M5-T5.6/F5
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
	var provider engine.ContextProvider = newContextProvider(rt.Store, rt.LosslessSwapping, config.ContextEngine{})
	evictor := engine.NewLadderEvictor()
	// A seam with nothing evictable reports an empty eviction, which is
	// the gate's pause path — not an error and not a bogus free count.
	got, err := evictor.Evict(context.Background(), "job-1", nil, nil)
	if err != nil || len(got.Tiers) != 0 || got.FreedTokens != 0 {
		t.Fatalf("Evict(no tiers) = (%+v, %v), want empty eviction", got, err)
	}
	// The gate is the runtime's own flag: enabled here, so the provider is
	// live (an empty store simply yields no working set).
	if _, ok, err := provider.ActiveChunk(context.Background(), engine.ContextQuery{JobID: "job-1"}); err != nil || ok {
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

	p := newContextProvider(cs, true, config.ContextEngine{})
	chunk, ok, err := p.ActiveChunk(ctx, engine.ContextQuery{JobID: "job-1"})
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

	index, err := p.DormantIndex(ctx, engine.ContextQuery{JobID: "job-1"})
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
	if _, ok, err := p.ActiveChunk(ctx, engine.ContextQuery{JobID: "job-other"}); err != nil || ok {
		t.Errorf("ActiveChunk(other) = (%v, %v), want (false, nil)", ok, err)
	}
	if idx, err := p.DormantIndex(ctx, engine.ContextQuery{JobID: "job-other"}); err != nil || len(idx) != 0 {
		t.Errorf("DormantIndex(other) = (%d, %v), want (0, nil)", len(idx), err)
	}
}

// TestContextProviderIncludesProjectIndex proves the F5 (ADR-0059) union: the
// Dormant Index carries the job's own chunks plus the project's repository
// chunks, and the project rows require a ProjectID.
func TestContextProviderIncludesProjectIndex(t *testing.T) {
	cs := migratedChunkStore(t)
	ctx := context.Background()
	jobChunks := division.New(division.Options{}).Divide("job.go", "go", []byte("package job\n\nfunc J() {}\n"))
	repoChunks := division.New(division.Options{}).Divide("repo.go", "go", []byte("package repo\n\nfunc R() {}\n"))
	if _, err := cs.PutSource(ctx, mce.SourceRef{RelPath: "job.go", JobID: "job-1", ProjectID: "p1"}, jobChunks); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutSource(ctx, mce.SourceRef{RelPath: "repo.go", ProjectID: "p1"}, repoChunks); err != nil {
		t.Fatal(err)
	}

	p := newContextProvider(cs, true, config.ContextEngine{})

	// With the project scope: both sources are present.
	index, err := p.DormantIndex(ctx, engine.ContextQuery{JobID: "job-1", ProjectID: "p1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, line := range index {
		paths[line.RelPath] = true
	}
	if !paths["job.go"] || !paths["repo.go"] {
		t.Fatalf("index paths = %v, want both job.go and repo.go", paths)
	}

	// Without the project scope: only the job's own chunks.
	only, err := p.DormantIndex(ctx, engine.ContextQuery{JobID: "job-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != len(jobChunks) {
		t.Fatalf("job-only rows = %d, want %d", len(only), len(jobChunks))
	}
	for _, line := range only {
		if line.RelPath != "job.go" {
			t.Errorf("job-only row = %q, want job.go", line.RelPath)
		}
	}
}

// TestContextProviderSeedsActiveChunk proves the opt-in tier-3 seed: with no
// active chunk and seeding on, the top-ranked project chunk becomes active;
// an oversized chunk is not seeded.
func TestContextProviderSeedsActiveChunk(t *testing.T) {
	cs := migratedChunkStore(t)
	ctx := context.Background()
	repoChunks := division.New(division.Options{}).Divide("repo.go", "go", []byte("package repo\n\nfunc R() {}\n"))
	if _, err := cs.PutSource(ctx, mce.SourceRef{RelPath: "repo.go", ProjectID: "p1"}, repoChunks); err != nil {
		t.Fatal(err)
	}

	on := newContextProvider(cs, true, config.ContextEngine{SeedActiveChunk: boolPtr(true), SeedActiveMaxBytes: 1 << 20})
	chunk, ok, err := on.ActiveChunk(ctx, engine.ContextQuery{JobID: "job-1", ProjectID: "p1"})
	if err != nil || !ok {
		t.Fatalf("seeded ActiveChunk = (%v, %v), want a seeded chunk", ok, err)
	}
	if chunk.RelPath != "repo.go" {
		t.Errorf("seeded chunk path = %q, want repo.go", chunk.RelPath)
	}
	// The seed persisted: a second call sees the active chunk without a
	// re-seed.
	again, ok, err := on.ActiveChunk(ctx, engine.ContextQuery{JobID: "job-1", ProjectID: "p1"})
	if err != nil || !ok || again.ID != chunk.ID {
		t.Errorf("second ActiveChunk = (%+v, %v, %v), want the same active chunk", again, ok, err)
	}

	// A one-byte cap refuses the seed.
	capped := newContextProvider(cs, true, config.ContextEngine{SeedActiveChunk: boolPtr(true), SeedActiveMaxBytes: 1})
	if _, ok, err := capped.ActiveChunk(ctx, engine.ContextQuery{JobID: "job-2", ProjectID: "p1"}); err != nil || ok {
		t.Errorf("oversized seed = (%v, %v), want no chunk", ok, err)
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

	disabled := newContextProvider(cs, false, config.ContextEngine{})
	if _, ok, err := disabled.ActiveChunk(ctx, engine.ContextQuery{JobID: "job-1"}); err != nil || ok {
		t.Errorf("disabled ActiveChunk = (%v, %v), want (false, nil)", ok, err)
	}
	if idx, err := disabled.DormantIndex(ctx, engine.ContextQuery{JobID: "job-1"}); err != nil || len(idx) != 0 {
		t.Errorf("disabled DormantIndex = (%d, %v), want (0, nil)", len(idx), err)
	}

	// A nil store (MCE not built) must behave the same way, not panic.
	empty := newContextProvider(nil, true, config.ContextEngine{})
	if _, ok, err := empty.ActiveChunk(ctx, engine.ContextQuery{JobID: "job-1"}); err != nil || ok {
		t.Errorf("nil-store ActiveChunk = (%v, %v), want (false, nil)", ok, err)
	}
	if idx, err := empty.DormantIndex(ctx, engine.ContextQuery{JobID: "job-1"}); err != nil || len(idx) != 0 {
		t.Errorf("nil-store DormantIndex = (%d, %v), want (0, nil)", len(idx), err)
	}
}
