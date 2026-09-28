package mce

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/mce/division"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// newStore opens a migrated temporary database behind a ChunkStore.
func newStore(t *testing.T) (*ChunkStore, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "athanor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if v := st.Version(); v < 9 {
		t.Fatalf("schema version = %d, want >= 9 (migration 0009 must exist)", v)
	}
	return NewChunkStore(st), st
}

// divide divides src and fails the test on a zero-chunk result.
func divide(t *testing.T, path, lang string, src []byte) []division.Chunk {
	t.Helper()
	chunks := division.New(division.Options{}).Divide(path, lang, src)
	if len(chunks) == 0 {
		t.Fatalf("divide(%s): zero chunks", path)
	}
	return chunks
}

const goSource = `package demo

import "fmt"

func A() { fmt.Println("a") }

func B() { return }
`

// TestPutSourceRoundTripByteExact is the store half of M5-T2's acceptance:
// chunks persisted to SQLite reassemble byte-for-byte.
func TestPutSourceRoundTripByteExact(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	srcs := []struct {
		path, lang string
		src        []byte
	}{
		{"demo.go", "go", []byte(goSource)},
		{"doc.md", "markdown", []byte("# Höhe\n\nÜber die Straße.\n\n## 二\n\ntext\n")},
		{"data.xyz", "", []byte("plain\nbytes\r\nwith stuff\n")},
	}
	for _, s := range srcs {
		chunks := divide(t, s.path, s.lang, s.src)
		if _, err := cs.PutSource(ctx, SourceRef{RelPath: s.path}, chunks); err != nil {
			t.Fatalf("%s: PutSource: %v", s.path, err)
		}
		got, err := cs.Reassemble(ctx, chunks[0].SourceHash)
		if err != nil {
			t.Fatalf("%s: Reassemble: %v", s.path, err)
		}
		if !bytes.Equal(got, s.src) {
			t.Fatalf("%s: reassembled %d bytes, want %d (byte-exactness violated)", s.path, len(got), len(s.src))
		}
	}
}

// TestPutSourceIsIdempotent proves deterministic chunk IDs make re-ingest a
// no-op that preserves an existing summary.
func TestPutSourceIsIdempotent(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	hash := chunks[0].SourceHash

	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}
	// Simulate the summarizer having filled one row.
	first := chunks[0].ID
	if _, err := cs.db.DB().ExecContext(ctx,
		`UPDATE dormant_index SET summary='first declaration', summary_status='ready' WHERE chunk_id=?`,
		first); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}

	recs, err := cs.ListBySource(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(chunks) {
		t.Fatalf("chunk count = %d after re-ingest, want %d", len(recs), len(chunks))
	}
	entries, err := cs.IndexForSource(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(chunks) {
		t.Fatalf("index rows = %d, want %d", len(entries), len(chunks))
	}
	if entries[0].Summary != "first declaration" || entries[0].SummaryStatus != "ready" {
		t.Errorf("re-ingest clobbered summary: %+v", entries[0])
	}
}

// TestIndexForSourceIsOrderedAndQueryable checks the Dormant Index contract:
// one row per chunk, byte-ordered, metadata-only, pending by default.
func TestIndexForSourceIsOrderedAndQueryable(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go", ProjectID: "p1"}, chunks); err != nil {
		t.Fatal(err)
	}
	entries, err := cs.IndexForSource(ctx, chunks[0].SourceHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(chunks) {
		t.Fatalf("index rows = %d, want %d", len(entries), len(chunks))
	}
	prev := -1
	for i, e := range entries {
		if e.ByteStart < prev {
			t.Fatalf("index not byte-ordered at %d", i)
		}
		prev = e.ByteStart
		if e.SummaryStatus != "pending" {
			t.Errorf("chunk %s status = %q, want pending", e.ChunkID, e.SummaryStatus)
		}
		if e.ChunkID != chunks[i].ID {
			t.Errorf("index row %d = %s, want %s", i, e.ChunkID, chunks[i].ID)
		}
	}
}

func TestGetAndReassembleUnknownAreNotFound(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	if _, err := cs.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown err = %v, want ErrNotFound", err)
	}
	if _, err := cs.Reassemble(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Reassemble unknown err = %v, want ErrNotFound", err)
	}
	recs, err := cs.ListBySource(ctx, "nope")
	if err != nil || len(recs) != 0 {
		t.Errorf("ListBySource unknown = (%v, %d); want (nil, 0)", err, len(recs))
	}
}

func TestVerifySourceHash(t *testing.T) {
	src := []byte(goSource)
	h := SourceHash(src)
	if err := VerifySourceHash(h, src); err != nil {
		t.Fatalf("VerifySourceHash(ok): %v", err)
	}
	var mismatch *ContentMismatchError
	if err := VerifySourceHash(h, []byte("different")); !errors.As(err, &mismatch) {
		t.Fatalf("VerifySourceHash(mismatch) = %v, want ContentMismatchError", err)
	}
}

func TestPutSourceRejectsMixedOrUnhashedChunks(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	a := divide(t, "a.go", "go", []byte(goSource))
	b := divide(t, "b.go", "go", []byte("package b\n\nfunc C() {}\n"))
	mixed := append(append([]division.Chunk{}, a...), b...)
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "x"}, mixed); err == nil {
		t.Error("PutSource accepted chunks from different sources")
	}
	noHash := []division.Chunk{{
		ID: "d", FilePath: "x", Lang: "go", Kind: division.KindAST,
		ByteStart: 0, ByteEnd: 1, Content: []byte("x"),
	}}
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "x"}, noHash); err == nil {
		t.Error("PutSource accepted a chunk without a source hash")
	}
}

func TestReassembleDetectsTamperedContent(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}
	// Corrupt one chunk's stored bytes behind the API's back.
	if _, err := cs.db.DB().ExecContext(ctx,
		`UPDATE context_chunks SET content=? WHERE id=?`,
		[]byte("corrupted"), chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	var mismatch *ContentMismatchError
	if _, err := cs.Reassemble(ctx, chunks[0].SourceHash); !errors.As(err, &mismatch) {
		t.Fatalf("Reassemble after tamper = %v, want ContentMismatchError", err)
	}
}
