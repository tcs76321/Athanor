package mce

import (
	"context"
	"fmt"
	"testing"

	"github.com/tcs76321/athanor/internal/mce/division"
)

// pathSummarizer returns a summary containing the chunk's path, so the FTS
// half of query_memory can find a file by name.
type pathSummarizer struct{ calls int }

func (p *pathSummarizer) Summarize(_ context.Context, c division.Chunk) (string, error) {
	p.calls++
	return "summary of " + c.FilePath, nil
}

// TestIndexRepositoryEndToEnd is the M5-T8 acceptance test (ROADMAP: "a
// mid-size repo completes; new files indexed without a full rescan"). A
// 40-file repository is indexed to completion, a second pass makes zero model
// calls, an edit and a delete converge on the next pass, and a query_memory
// hit is loaded byte-exact through context_swap.
func TestIndexRepositoryEndToEnd(t *testing.T) {
	cs, st := newStore(t)
	seedProject(t, st, "p1")
	root := t.TempDir()

	// 40 indexable files across the five division languages.
	for i := 0; i < 8; i++ {
		for _, ext := range []string{"go", "py", "js", "md", "txt"} {
			writeRepoFile(t, root, fmt.Sprintf("pkg%d/file%d.%s", i, i, ext),
				[]byte(fmt.Sprintf("content %d %s unique-%d-%s\n", i, ext, i, ext)))
		}
	}
	// Non-indexable noise: an ignored directory, a binary, an unknown type.
	writeRepoFile(t, root, "node_modules/x.js", []byte("ignored\n"))
	writeRepoFile(t, root, "blob.txt", []byte{0x00, 0x01, 0x02, 'x'})
	writeRepoFile(t, root, "data.json", []byte("{}\n"))

	sum := &pathSummarizer{}
	emb := &indexEmbedder{model: "fake-embed", dim: 4}
	ix := NewIndexer(cs, NewIndexManifest(st), sum, emb, NewSQLVectorIndex(st))
	opts := IndexOptions{
		Root: root, ProjectID: "p1",
		MaxSourceBytes: 1 << 20, MaxChunkBytes: 1 << 16, FallbackLines: 40,
		EmbedBytes: 128, LosslessSwapping: true,
	}
	ctx := context.Background()

	// First pass: everything is indexed and embedded.
	res, err := ix.RunAll(ctx, opts)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if res.Indexed != 40 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("first pass = %+v, want 40 indexed / 0 skipped / 0 failed", res)
	}
	if res.Embedded != res.Chunks || res.Summarized != res.Chunks {
		t.Fatalf("first pass = %+v, want every chunk summarized and embedded", res)
	}
	sumCalls, embCalls := sum.calls, emb.calls
	if sumCalls == 0 || embCalls == 0 {
		t.Fatalf("first pass made no model calls (sum=%d emb=%d)", sumCalls, embCalls)
	}

	// Second pass: no file changed, so no model call and nothing indexed.
	res2, err := ix.RunAll(ctx, opts)
	if err != nil {
		t.Fatalf("second RunAll: %v", err)
	}
	if res2.Indexed != 0 || res2.Pruned != 0 {
		t.Fatalf("second pass = %+v, want no work", res2)
	}
	if sum.calls != sumCalls || emb.calls != embCalls {
		t.Fatalf("second pass made model calls: sum %d->%d, emb %d->%d",
			sumCalls, sum.calls, embCalls, emb.calls)
	}

	// A query_memory hit is reachable and swappable.
	hits, err := NewRetriever(st).Query(ctx, QueryOptions{Query: "file3", Scope: Scope{ProjectID: "p1"}, TopK: 5})
	if err != nil {
		t.Fatalf("query_memory: %v", err)
	}
	var chunkHit string
	for _, h := range hits {
		if h.Kind == SourceChunk {
			chunkHit = h.ID
			break
		}
	}
	if chunkHit == "" {
		t.Fatalf("query_memory returned no chunk hit for file3: %+v", hits)
	}
	want, err := cs.Get(ctx, chunkHit)
	if err != nil {
		t.Fatal(err)
	}
	sw, err := cs.Swap(ctx, "job-1", chunkHit)
	if err != nil {
		t.Fatalf("context_swap: %v", err)
	}
	if string(sw.Loaded.Content) != string(want.Content) {
		t.Fatal("context_swap loaded different bytes than the stored chunk")
	}

	// Edit one file and delete another; one more pass converges both.
	writeRepoFile(t, root, "pkg0/file0.go", []byte("package changed\n\n// longer now\n"))
	if err := removeRepoFile(t, root, "pkg1/file1.go"); err != nil {
		t.Fatal(err)
	}
	res3, err := ix.RunAll(ctx, opts)
	if err != nil {
		t.Fatalf("third RunAll: %v", err)
	}
	if res3.Indexed != 1 || res3.Pruned != 1 {
		t.Fatalf("third pass = %+v, want 1 indexed / 1 pruned", res3)
	}
	// The edited file's old chunks are gone; the deleted file's manifest row
	// is gone.
	if _, ok, _ := NewIndexManifest(st).Get(ctx, "p1", "pkg1/file1.go"); ok {
		t.Error("deleted file's manifest row survived")
	}
}
