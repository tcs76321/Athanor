package mce

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// openStoreAt opens (and migrates) a store at a fixed path so a test can close
// and reopen the same database.
func openStoreAt(t *testing.T, path string) (*ChunkStore, *store.Store) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewChunkStore(st), st
}

// TestSwapLoadsByteExactAndFlushesIntact is the M5-T3 acceptance test: a swap
// loads the target chunk byte-for-byte and returns the prior active chunk
// intact (and still intact in storage).
func TestSwapLoadsByteExactAndFlushesIntact(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if len(chunks) < 2 {
		t.Fatalf("need >= 2 chunks, got %d", len(chunks))
	}
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}

	first, err := cs.Swap(ctx, "job-1", chunks[0].ID)
	if err != nil {
		t.Fatalf("first Swap: %v", err)
	}
	if first.Flushed != nil {
		t.Errorf("first swap flushed %+v, want nothing", first.Flushed)
	}
	if !bytes.Equal(first.Loaded.Content, chunks[0].Content) {
		t.Fatal("loaded chunk is not byte-exact")
	}

	second, err := cs.Swap(ctx, "job-1", chunks[1].ID)
	if err != nil {
		t.Fatalf("second Swap: %v", err)
	}
	if second.Flushed == nil {
		t.Fatal("second swap did not return the flushed chunk")
	}
	if second.Flushed.ID != chunks[0].ID {
		t.Errorf("flushed %s, want %s", second.Flushed.ID, chunks[0].ID)
	}
	if !bytes.Equal(second.Flushed.Content, chunks[0].Content) {
		t.Error("flushed chunk is not intact")
	}
	if !bytes.Equal(second.Loaded.Content, chunks[1].Content) {
		t.Error("loaded chunk is not byte-exact")
	}

	// The flushed chunk is unchanged in storage too.
	stored, err := cs.Get(ctx, chunks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.Content, chunks[0].Content) {
		t.Error("flushed chunk changed in storage")
	}

	// The active pointer now names the loaded chunk.
	active, ok, err := cs.Active(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || active.ID != chunks[1].ID {
		t.Fatalf("active = (%s, %v), want %s", active.ID, ok, chunks[1].ID)
	}
}

func TestSwapNoOpWhenAlreadyActive(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Swap(ctx, "s", chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	res, err := cs.Swap(ctx, "s", chunks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NoOp {
		t.Error("swapping to the active chunk should be a no-op")
	}
	if res.Flushed != nil {
		t.Error("a no-op swap must not flush")
	}
}

// TestActiveSurvivesRestart proves the §23.6 crash-resume property: the active
// mapping is persisted, so a restart resumes with the same chunk, byte-exact.
func TestActiveSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "athanor.db")

	cs, st := openStoreAt(t, path)
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Swap(ctx, "job-1", chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	cs2, _ := openStoreAt(t, path)
	rec, ok, err := cs2.Active(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("active chunk lost across restart")
	}
	if rec.ID != chunks[0].ID {
		t.Errorf("active = %s, want %s", rec.ID, chunks[0].ID)
	}
	if !bytes.Equal(rec.Content, chunks[0].Content) {
		t.Error("restored active chunk is not byte-exact")
	}
}

func TestActiveAbsentForFreshScope(t *testing.T) {
	cs, _ := newStore(t)
	if _, ok, err := cs.Active(context.Background(), "never-used"); err != nil || ok {
		t.Fatalf("Active(fresh) = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

func TestSwapRejectsBadInput(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	if _, err := cs.Swap(ctx, "", "x"); err == nil {
		t.Error("empty scope accepted")
	}
	if _, err := cs.Swap(ctx, "s", ""); err == nil {
		t.Error("empty target accepted")
	}
	if _, err := cs.Swap(ctx, "s", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown target err = %v, want ErrNotFound", err)
	}
}
