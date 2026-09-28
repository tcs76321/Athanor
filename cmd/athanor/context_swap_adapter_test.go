package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/mce/division"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
	"github.com/tcs76321/athanor/migrations"
)

const adapterTestSource = `package a

import "fmt"

func A() { fmt.Println("a") }

func B() { return }
`

// newAdapterHarness returns an adapter over a migrated temp store holding one
// divided source, so swaps have real chunks to rotate.
func newAdapterHarness(t *testing.T) (*contextSwapAdapter, []division.Chunk) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "athanor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cs := mce.NewChunkStore(st)
	chunks := division.New(division.Options{}).Divide("a.go", "go", []byte(adapterTestSource))
	if len(chunks) < 2 {
		t.Fatalf("need >= 2 chunks, got %d", len(chunks))
	}
	if _, err := cs.PutSource(context.Background(), mce.SourceRef{RelPath: "a.go"}, chunks); err != nil {
		t.Fatalf("PutSource: %v", err)
	}
	return newContextSwapAdapter(cs, true), chunks
}

func TestContextSwapAdapter_ReturnsByteExactChunk(t *testing.T) {
	ctx := context.Background()
	ad, chunks := newAdapterHarness(t)

	first, err := ad.SwapContext(ctx, toolenvelope.ContextSwapRequest{Scope: "job-1", TargetChunkID: chunks[0].ID})
	if err != nil {
		t.Fatalf("SwapContext: %v", err)
	}
	if first.Scope != "job-1" {
		t.Errorf("scope = %q, want job-1", first.Scope)
	}
	if first.LoadedChunkID != chunks[0].ID {
		t.Errorf("loaded = %s, want %s", first.LoadedChunkID, chunks[0].ID)
	}
	if first.LoadedContent != string(chunks[0].Content) {
		t.Error("loaded content is not byte-exact")
	}
	if first.LoadedBytes != len(chunks[0].Content) {
		t.Errorf("LoadedBytes = %d, want %d", first.LoadedBytes, len(chunks[0].Content))
	}
	if first.FlushedChunkID != "" {
		t.Errorf("FlushedChunkID = %q, want empty on the first swap", first.FlushedChunkID)
	}

	second, err := ad.SwapContext(ctx, toolenvelope.ContextSwapRequest{Scope: "job-1", TargetChunkID: chunks[1].ID})
	if err != nil {
		t.Fatalf("second SwapContext: %v", err)
	}
	if second.FlushedChunkID != chunks[0].ID {
		t.Errorf("flushed = %s, want %s", second.FlushedChunkID, chunks[0].ID)
	}
	if second.LoadedContent != string(chunks[1].Content) {
		t.Error("second loaded content is not byte-exact")
	}
}

func TestContextSwapAdapter_UnknownChunkIsNotFound(t *testing.T) {
	ad, _ := newAdapterHarness(t)
	_, err := ad.SwapContext(context.Background(),
		toolenvelope.ContextSwapRequest{Scope: "s", TargetChunkID: "does-not-exist"})
	if !errors.Is(err, internalapi.ErrChunkNotFound) {
		t.Fatalf("err = %v, want internalapi.ErrChunkNotFound", err)
	}
}

func TestContextSwapAdapter_DisabledRefuses(t *testing.T) {
	ad := newContextSwapAdapter(nil, false)
	_, err := ad.SwapContext(context.Background(),
		toolenvelope.ContextSwapRequest{Scope: "s", TargetChunkID: "x"})
	if !errors.Is(err, internalapi.ErrSwapDisabled) {
		t.Fatalf("err = %v, want internalapi.ErrSwapDisabled", err)
	}
}
