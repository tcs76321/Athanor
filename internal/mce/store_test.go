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

// TestIndexForJobIsScopedToTheJob is the M5-T5.4 contract: the Dormant
// Index an assembler publishes for a job contains that job's chunks only —
// not its project's other jobs, not un-attributed rows, not another job's.
func TestIndexForJobIsScopedToTheJob(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()

	// Two files for job-1, one file for job-2 in the same project, plus an
	// un-attributed file. Each file carries distinct bytes: chunk IDs are
	// content-derived (ADR-0021 §5), so identical files would collapse
	// into one chunk set and prove nothing about scoping.
	job1a := divide(t, "a.go", "go", []byte("package demo\n\nfunc A() {}\n"))
	job1b := divide(t, "b.go", "go", []byte("package demo\n\nfunc B() {}\n"))
	job2 := divide(t, "c.go", "go", []byte("package demo\n\nfunc C() {}\n"))
	orphan := divide(t, "d.go", "go", []byte("package demo\n\nfunc D() {}\n"))
	for _, put := range []struct {
		ref    SourceRef
		chunks []division.Chunk
	}{
		{SourceRef{RelPath: "a.go", ProjectID: "p1", JobID: "job-1"}, job1a},
		{SourceRef{RelPath: "b.go", ProjectID: "p1", JobID: "job-1"}, job1b},
		{SourceRef{RelPath: "c.go", ProjectID: "p1", JobID: "job-2"}, job2},
		{SourceRef{RelPath: "d.go", ProjectID: "p1"}, orphan},
	} {
		if _, err := cs.PutSource(ctx, put.ref, put.chunks); err != nil {
			t.Fatalf("PutSource(%s): %v", put.ref.RelPath, err)
		}
	}

	entries, err := cs.IndexForJob(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := len(job1a) + len(job1b); len(entries) != want {
		t.Fatalf("job-1 index rows = %d, want %d (both its sources, nothing else)", len(entries), want)
	}
	prevPath, prevStart := "", 0
	for i, e := range entries {
		if e.SourceRelPath != "a.go" && e.SourceRelPath != "b.go" {
			t.Errorf("job-1 index leaked %s (belongs to another scope)", e.SourceRelPath)
		}
		if i > 0 && e.SourceRelPath == prevPath && e.ByteStart < prevStart {
			t.Errorf("job-1 index not byte-ordered within %s", e.SourceRelPath)
		}
		prevPath, prevStart = e.SourceRelPath, e.ByteStart
	}

	// An unknown job and an empty scope both yield nothing, without error.
	for _, jobID := range []string{"job-does-not-exist", ""} {
		got, err := cs.IndexForJob(ctx, jobID)
		if err != nil {
			t.Errorf("IndexForJob(%q): %v", jobID, err)
		}
		if len(got) != 0 {
			t.Errorf("IndexForJob(%q) = %d rows, want 0", jobID, len(got))
		}
	}
}

// TestMigration0011Applies pins that the job index migration is present
// and idempotent (the forward-only runner re-applies nothing), and that
// existing rows survive it — it is an index-only change.
func TestMigration0011Applies(t *testing.T) {
	cs, st := newStore(t)
	if v := st.Version(); v < 11 {
		t.Fatalf("schema version = %d, want >= 11 (migration 0011 must exist)", v)
	}
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if _, err := cs.PutSource(context.Background(),
		SourceRef{RelPath: "demo.go", JobID: "job-keep"}, chunks); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("re-running migrations: %v", err)
	}
	entries, err := cs.IndexForJob(context.Background(), "job-keep")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(chunks) {
		t.Errorf("index rows after re-migrate = %d, want %d (rows must survive)", len(entries), len(chunks))
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
