package mce

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

// removeRepoFile deletes one file from the test repository.
func removeRepoFile(t *testing.T, root, rel string) error {
	t.Helper()
	return os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
}

// indexEmbedder is a deterministic, call-counting Embedder for the indexer
// tests (the query tests' fakeEmbedder has no counter).
type indexEmbedder struct {
	model string
	dim   int
	calls int
}

func (f *indexEmbedder) Model() string { return f.model }

func (f *indexEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(texts))
	for i, txt := range texts {
		v := make([]float32, f.dim)
		for j := range v {
			v[j] = float32((len(txt)+j)%7) + 0.5
		}
		out[i] = v
	}
	return out, nil
}

type indexHarness struct {
	chunks   *ChunkStore
	manifest *IndexManifest
	sum      *fakeSummarizer
	emb      *indexEmbedder
	idx      *Indexer
	st       *store.Store
}

func newIndexHarness(t *testing.T, withVector bool) *indexHarness {
	t.Helper()
	cs, st := newStore(t)
	seedProject(t, st, "p1")
	h := &indexHarness{
		chunks:   cs,
		manifest: NewIndexManifest(st),
		sum:      &fakeSummarizer{summary: "a one-line summary"},
		st:       st,
	}
	if withVector {
		h.emb = &indexEmbedder{model: "fake-embed", dim: 4}
		h.idx = NewIndexer(cs, h.manifest, h.sum, h.emb, NewSQLVectorIndex(st))
	} else {
		h.idx = NewIndexer(cs, h.manifest, h.sum, nil, nil)
	}
	return h
}

func (h *indexHarness) opts(root string) IndexOptions {
	return IndexOptions{
		Root: root, ProjectID: "p1",
		MaxSourceBytes: 1 << 20, MaxChunkBytes: 1 << 16, FallbackLines: 40,
		EmbedBytes: 128, LosslessSwapping: true,
	}
}

func (h *indexHarness) embeddings(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.st.DB().QueryRow(`SELECT COUNT(*) FROM memory_embeddings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIndexFirstPassIndexesAndEmbeds(t *testing.T) {
	h := newIndexHarness(t, true)
	root := t.TempDir()
	writeRepoFile(t, root, "main.go", []byte("package main\n\nfunc main() {}\n"))
	writeRepoFile(t, root, "docs/guide.md", []byte("# Guide\n\nSome prose.\n"))
	writeRepoFile(t, root, "notes.txt", []byte("plain notes\n"))

	res, err := h.idx.RunOnce(context.Background(), h.opts(root))
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.Indexed != 3 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("res = %+v, want 3 indexed / 0 skipped / 0 failed", res)
	}
	if res.Chunks < 3 {
		t.Fatalf("chunks = %d, want >= 3", res.Chunks)
	}
	if res.Summarized != res.Chunks {
		t.Errorf("summarized = %d, want %d", res.Summarized, res.Chunks)
	}
	if res.Embedded != res.Chunks {
		t.Errorf("embedded = %d, want %d", res.Embedded, res.Chunks)
	}
	if h.sum.calls != res.Chunks {
		t.Errorf("summarizer calls = %d, want %d", h.sum.calls, res.Chunks)
	}
	if got := h.embeddings(t); got != res.Chunks {
		t.Errorf("memory_embeddings = %d, want %d", got, res.Chunks)
	}

	// The divided source reassembles byte-for-byte.
	row, ok, err := h.manifest.Get(context.Background(), "p1", "main.go")
	if err != nil || !ok {
		t.Fatalf("manifest Get: ok=%v err=%v", ok, err)
	}
	got, err := h.chunks.Reassemble(context.Background(), row.SourceHash)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if string(got) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("reassembled = %q", got)
	}
}

func TestIndexSecondPassSkipsUnchanged(t *testing.T) {
	h := newIndexHarness(t, true)
	root := t.TempDir()
	writeRepoFile(t, root, "a.go", []byte("package a\n"))
	writeRepoFile(t, root, "b.py", []byte("x = 1\n"))

	if _, err := h.idx.RunOnce(context.Background(), h.opts(root)); err != nil {
		t.Fatal(err)
	}
	h.sum.calls, h.emb.calls = 0, 0
	res, err := h.idx.RunOnce(context.Background(), h.opts(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.Indexed != 0 || res.Skipped != 2 {
		t.Fatalf("second pass = %+v, want 0 indexed / 2 skipped", res)
	}
	if h.sum.calls != 0 || h.emb.calls != 0 {
		t.Fatalf("second pass made model calls: sum=%d emb=%d, want 0/0", h.sum.calls, h.emb.calls)
	}
}

func TestIndexEditReindexesAndPrunesOldSource(t *testing.T) {
	h := newIndexHarness(t, true)
	root := t.TempDir()
	writeRepoFile(t, root, "a.go", []byte("package a\n"))
	writeRepoFile(t, root, "b.go", []byte("package b\n"))
	ctx := context.Background()
	if _, err := h.idx.RunOnce(ctx, h.opts(root)); err != nil {
		t.Fatal(err)
	}
	old, _, err := h.manifest.Get(ctx, "p1", "a.go")
	if err != nil {
		t.Fatal(err)
	}

	writeRepoFile(t, root, "a.go", []byte("package a\n\n// changed and longer\n"))
	res, err := h.idx.RunOnce(ctx, h.opts(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.Indexed != 1 || res.Skipped != 1 {
		t.Fatalf("edit pass = %+v, want 1 indexed / 1 skipped", res)
	}
	if _, err := h.chunks.ListBySource(ctx, old.SourceHash); err != nil {
		t.Fatal(err)
	}
	if recs, _ := h.chunks.ListBySource(ctx, old.SourceHash); len(recs) != 0 {
		t.Errorf("old source still has %d chunks, want 0", len(recs))
	}
	neu, _, err := h.manifest.Get(ctx, "p1", "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if neu.SourceHash == old.SourceHash {
		t.Error("manifest hash unchanged after content edit")
	}
}

func TestIndexDeletePrunes(t *testing.T) {
	h := newIndexHarness(t, true)
	root := t.TempDir()
	writeRepoFile(t, root, "a.go", []byte("package a\n"))
	writeRepoFile(t, root, "b.go", []byte("package b\n"))
	ctx := context.Background()
	if _, err := h.idx.RunOnce(ctx, h.opts(root)); err != nil {
		t.Fatal(err)
	}
	if err := removeRepoFile(t, root, "a.go"); err != nil {
		t.Fatal(err)
	}
	res, err := h.idx.RunOnce(ctx, h.opts(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.Pruned != 1 {
		t.Fatalf("delete pass = %+v, want 1 pruned", res)
	}
	if _, ok, _ := h.manifest.Get(ctx, "p1", "a.go"); ok {
		t.Error("manifest row for deleted file still present")
	}
	before := h.embeddings(t)
	// One file remains, so its embeddings must have survived the prune.
	if before == 0 {
		t.Error("all embeddings were pruned, want the surviving file's kept")
	}
}

func TestIndexBatchesAndCatchesUp(t *testing.T) {
	h := newIndexHarness(t, false)
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		writeRepoFile(t, root, name, []byte("package p\n"))
	}
	opts := h.opts(root)
	opts.MaxFiles = 2
	ctx := context.Background()

	first, err := h.idx.RunOnce(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Indexed != 2 {
		t.Fatalf("first bounded pass indexed = %d, want 2", first.Indexed)
	}
	total, err := h.idx.RunAll(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if total.Indexed != 3 {
		t.Fatalf("RunAll after first pass indexed = %d, want 3 remaining", total.Indexed)
	}
	// A final full pass is all-skip.
	h.sum.calls = 0
	res, err := h.idx.RunOnce(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Indexed != 0 || res.Skipped != 5 || h.sum.calls != 0 {
		t.Fatalf("caught-up pass = %+v (calls=%d), want 0 indexed / 5 skipped / 0 calls", res, h.sum.calls)
	}
}

func TestIndexVectorInertWithoutEmbedder(t *testing.T) {
	h := newIndexHarness(t, false)
	root := t.TempDir()
	writeRepoFile(t, root, "a.go", []byte("package a\n"))
	res, err := h.idx.RunOnce(context.Background(), h.opts(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.Indexed != 1 || res.Summarized == 0 {
		t.Fatalf("res = %+v, want indexed with summaries", res)
	}
	if res.Embedded != 0 {
		t.Errorf("embedded = %d, want 0 without an embedder", res.Embedded)
	}
	if got := h.embeddings(t); got != 0 {
		t.Errorf("memory_embeddings = %d, want 0", got)
	}
}

func TestIndexSummarizerFailureIsRetried(t *testing.T) {
	h := newIndexHarness(t, true)
	root := t.TempDir()
	writeRepoFile(t, root, "a.go", []byte("package a\n"))
	ctx := context.Background()

	h.sum.err = errors.New("summarizer unavailable")
	first, err := h.idx.RunOnce(ctx, h.opts(root))
	if err != nil {
		t.Fatal(err)
	}
	if first.Failed != 1 || first.Indexed != 0 {
		t.Fatalf("failing pass = %+v, want 1 failed / 0 indexed", first)
	}
	row, ok, err := h.manifest.Get(ctx, "p1", "a.go")
	if err != nil || !ok {
		t.Fatalf("manifest Get: ok=%v err=%v", ok, err)
	}
	if row.Status != "failed" {
		t.Fatalf("status = %q, want failed", row.Status)
	}

	h.sum.err = nil
	second, err := h.idx.RunOnce(ctx, h.opts(root))
	if err != nil {
		t.Fatal(err)
	}
	if second.Indexed != 1 {
		t.Fatalf("recovery pass = %+v, want 1 indexed", second)
	}
	row, _, _ = h.manifest.Get(ctx, "p1", "a.go")
	if row.Status != "ok" {
		t.Fatalf("status after recovery = %q, want ok", row.Status)
	}
}

func TestIndexPruneKeepsSharedChunks(t *testing.T) {
	h := newIndexHarness(t, true)
	root := t.TempDir()
	content := []byte("package shared\n")
	writeRepoFile(t, root, "a.go", content)
	writeRepoFile(t, root, "copy.go", content)
	ctx := context.Background()

	if _, err := h.idx.RunOnce(ctx, h.opts(root)); err != nil {
		t.Fatal(err)
	}
	row, _, err := h.manifest.Get(ctx, "p1", "a.go")
	if err != nil {
		t.Fatal(err)
	}
	hash := row.SourceHash

	// Deleting one duplicate must not strip the shared chunks.
	if err := removeRepoFile(t, root, "copy.go"); err != nil {
		t.Fatal(err)
	}
	res, err := h.idx.RunOnce(ctx, h.opts(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.Pruned != 1 {
		t.Fatalf("prune pass = %+v, want 1 pruned", res)
	}
	if recs, _ := h.chunks.ListBySource(ctx, hash); len(recs) == 0 {
		t.Fatal("shared chunks were pruned while a referencing path remained")
	}

	// Deleting the last referrer prunes them.
	if err := removeRepoFile(t, root, "a.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.idx.RunOnce(ctx, h.opts(root)); err != nil {
		t.Fatal(err)
	}
	if recs, _ := h.chunks.ListBySource(ctx, hash); len(recs) != 0 {
		t.Fatalf("last referrer deleted but %d chunks remain", len(recs))
	}
}

func TestIndexRequiresSwappingAndScope(t *testing.T) {
	h := newIndexHarness(t, false)
	root := t.TempDir()
	opts := h.opts(root)
	opts.LosslessSwapping = false
	if _, err := h.idx.RunOnce(context.Background(), opts); !errors.Is(err, ErrSwappingDisabled) {
		t.Fatalf("RunOnce(swapping off) = %v, want ErrSwappingDisabled", err)
	}
	opts = h.opts(root)
	opts.ProjectID = ""
	if _, err := h.idx.RunOnce(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("RunOnce(no project) = %v, want project error", err)
	}
}
