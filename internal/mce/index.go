package mce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/store"
)

// Repository indexing pipeline (ROADMAP M5-T8; ADR-0028 §3–§4).
//
// The indexer is the producer the MCE lacked: it walks a repository, divides
// and stores each indexable file, refreshes the Dormant Index summaries, and
// embeds a bounded digest of every summarized chunk. It is incremental (a
// manifest row lets an unchanged file be skipped without being read) and
// bounded (a pass processes at most MaxFiles files and roughly MaxChunks
// model calls).
//
// internal/mce must not import internal/llm: the summarizer and embedder are
// injected seams whose adapters live in cmd/ (ADR-0021 §2, ADR-0026 §3).

const (
	// DefaultIndexFiles bounds files processed by one pass.
	DefaultIndexFiles = 50
	// DefaultIndexChunks bounds summary + embedding model calls per pass.
	DefaultIndexChunks = 200
	// DefaultEmbedBytes is the content prefix included in an embedding
	// digest. The config layer supplies it; an IndexOptions zero value
	// means path + summary only.
	DefaultEmbedBytes = 2048
)

// IndexOptions bounds one indexing pass.
type IndexOptions struct {
	// Root is the repository directory walked. ProjectID scopes every
	// chunk, summary, and embedding (ADR-0028 §1).
	Root      string
	ProjectID string
	// MaxFiles and MaxChunks bound a pass; ≤ 0 selects the defaults.
	MaxFiles  int
	MaxChunks int
	// MaxSourceBytes, MaxChunkBytes, and FallbackLines are the §10.1
	// division bounds (chunk ingestion); MaxSourceBytes is also the walker
	// cap.
	MaxSourceBytes int64
	MaxChunkBytes  int
	FallbackLines  int
	// EmbedBytes is the content-prefix size in the embedding digest; a
	// negative value means 0 (path + summary only).
	EmbedBytes  int
	ExcludeDirs []string
	// LosslessSwapping must be true; false refuses like IngestFile does.
	LosslessSwapping bool
}

// IndexResult counts one pass.
type IndexResult struct {
	Discovered int
	Indexed    int
	Skipped    int
	Pruned     int
	Chunks     int
	Summarized int
	Embedded   int
	Failed     int
}

// Indexer orchestrates division, summarization, and embedding. summarizer,
// embed, and vectors are optional seams: a nil summarizer skips summaries
// (and therefore embeddings); a nil/empty embedder leaves the vector half
// inert and indexing is division + summaries only (ADR-0028 §3).
type Indexer struct {
	chunks     *ChunkStore
	manifest   *IndexManifest
	summarizer Summarizer
	embed      Embedder
	vectors    VectorIndex
}

// NewIndexer wires an indexer over the daemon's stores and model seams.
func NewIndexer(chunks *ChunkStore, manifest *IndexManifest, summarizer Summarizer, embed Embedder, vectors VectorIndex) *Indexer {
	return &Indexer{chunks: chunks, manifest: manifest, summarizer: summarizer, embed: embed, vectors: vectors}
}

// vectorEnabled reports whether the embedding half is configured.
func (ix *Indexer) vectorEnabled() bool {
	return ix.embed != nil && ix.vectors != nil && ix.embed.Model() != ""
}

// RunOnce performs one bounded pass. It returns when every discovered file
// has been handled or a bound is reached; a later pass continues. Files
// deleted from the repository have their manifest row and derived rows
// pruned.
func (ix *Indexer) RunOnce(ctx context.Context, opts IndexOptions) (IndexResult, error) {
	if !opts.LosslessSwapping {
		return IndexResult{}, ErrSwappingDisabled
	}
	if opts.Root == "" || opts.ProjectID == "" {
		return IndexResult{}, errors.New("mce: indexing requires a root and a project id")
	}
	files, err := Discover(opts.Root, WalkOptions{ExcludeDirs: opts.ExcludeDirs, MaxSourceBytes: opts.MaxSourceBytes})
	if err != nil {
		return IndexResult{}, fmt.Errorf("mce: discover %s: %w", opts.Root, err)
	}
	res := IndexResult{Discovered: len(files)}

	known, err := ix.manifest.List(ctx, opts.ProjectID)
	if err != nil {
		return IndexResult{}, err
	}
	byPath := make(map[string]SourceRow, len(known))
	for _, r := range known {
		byPath[r.RelPath] = r
	}
	present := make(map[string]bool, len(files))
	for _, f := range files {
		present[f.RelPath] = true
	}

	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultIndexFiles
	}
	maxChunks := opts.MaxChunks
	if maxChunks <= 0 {
		maxChunks = DefaultIndexChunks
	}

	modelCalls, attempted := 0, 0
	for _, f := range files {
		if attempted >= maxFiles {
			break
		}
		if modelCalls >= maxChunks && attempted > 0 {
			break
		}
		if row, ok := byPath[f.RelPath]; ok && row.Status == "ok" && row.Size == f.Size && row.MTimeUnix == f.MTimeUnix {
			res.Skipped++
			continue
		}
		attempted++
		out, ierr := ix.indexFile(ctx, f, opts)
		res.Chunks += out.chunks
		res.Summarized += out.summarized
		res.Embedded += out.embedded
		modelCalls += out.summarized + out.embedded
		if ierr != nil {
			res.Failed++
			_ = ix.manifest.Put(ctx, SourceRow{
				ProjectID: opts.ProjectID, RelPath: f.RelPath, Lang: f.Lang,
				Size: f.Size, MTimeUnix: f.MTimeUnix, SourceHash: out.sourceHash,
				Chunks: out.chunks, Status: "failed", Error: ierr.Error(),
			})
			ix.audit(ctx, opts.ProjectID, map[string]any{
				"event": "source_index_failed", "source": f.RelPath, "error": ierr.Error(),
			})
			continue
		}
		res.Indexed++
	}

	// Prune paths that are no longer present in the repository.
	for relpath, row := range byPath {
		if present[relpath] {
			continue
		}
		if err := ix.pruneUnshared(ctx, opts.ProjectID, row.SourceHash); err != nil {
			return res, err
		}
		if err := ix.manifest.Delete(ctx, opts.ProjectID, relpath); err != nil {
			return res, err
		}
		res.Pruned++
	}

	if res.Indexed > 0 || res.Pruned > 0 || res.Failed > 0 {
		ix.audit(ctx, opts.ProjectID, map[string]any{
			"event": "index_pass", "discovered": res.Discovered, "indexed": res.Indexed,
			"skipped": res.Skipped, "pruned": res.Pruned, "chunks": res.Chunks,
			"summarized": res.Summarized, "embedded": res.Embedded, "failed": res.Failed,
		})
	}
	return res, nil
}

// RunAll loops RunOnce until the repository is caught up (no file indexed or
// pruned). It is the CLI's synchronous entry point; the idle driver calls
// RunOnce so a single pass yields to real work.
func (ix *Indexer) RunAll(ctx context.Context, opts IndexOptions) (IndexResult, error) {
	var total IndexResult
	for {
		r, err := ix.RunOnce(ctx, opts)
		if err != nil {
			return total, err
		}
		if r.Indexed == 0 && r.Pruned == 0 {
			total.Discovered = r.Discovered
			return total, nil
		}
		total.Discovered = r.Discovered
		total.Indexed += r.Indexed
		total.Skipped += r.Skipped
		total.Pruned += r.Pruned
		total.Chunks += r.Chunks
		total.Summarized += r.Summarized
		total.Embedded += r.Embedded
		total.Failed += r.Failed
	}
}

// fileOutcome is what indexing one file produced (sourceHash is set as soon
// as it is known so a failed file can still be recorded).
type fileOutcome struct {
	sourceHash string
	chunks     int
	summarized int
	embedded   int
}

// indexFile divides and stores one file, supersedes its previous revision,
// fills summaries, and embeds ready chunks.
func (ix *Indexer) indexFile(ctx context.Context, f FileRef, opts IndexOptions) (fileOutcome, error) {
	ingest, err := ix.chunks.IngestFile(ctx, opts.Root, f.RelPath, IngestOptions{
		MaxSourceBytes:   opts.MaxSourceBytes,
		MaxChunkBytes:    opts.MaxChunkBytes,
		FallbackLines:    opts.FallbackLines,
		LosslessSwapping: true,
	}, SourceRef{ProjectID: opts.ProjectID, RelPath: f.RelPath})
	if err != nil {
		return fileOutcome{}, err
	}
	if ingest.Skipped {
		return fileOutcome{}, fmt.Errorf("source skipped after discovery: %s", ingest.Reason)
	}
	out := fileOutcome{sourceHash: ingest.SourceHash, chunks: ingest.Chunks}

	// Supersede the previous revision of this path.
	old, seen, err := ix.manifest.Get(ctx, opts.ProjectID, f.RelPath)
	if err != nil {
		return out, err
	}
	if seen && old.SourceHash != "" && old.SourceHash != out.sourceHash {
		if err := ix.pruneUnshared(ctx, opts.ProjectID, old.SourceHash); err != nil {
			return out, err
		}
	}

	if ix.summarizer != nil {
		enr, err := ix.chunks.Enrich(ctx, out.sourceHash, ix.summarizer)
		if err != nil {
			return out, err
		}
		out.summarized = enr.Enriched
		if enr.Failed > 0 {
			return out, fmt.Errorf("summarizer failed for %d chunk(s)", enr.Failed)
		}
	}

	embedded, err := ix.embedSource(ctx, out.sourceHash, opts)
	out.embedded = embedded
	if err != nil {
		return out, err
	}

	if err := ix.manifest.Put(ctx, SourceRow{
		ProjectID: opts.ProjectID, RelPath: f.RelPath, Lang: f.Lang,
		Size: f.Size, MTimeUnix: f.MTimeUnix, SourceHash: out.sourceHash,
		Chunks: out.chunks, Status: "ok",
	}); err != nil {
		return out, err
	}
	ix.audit(ctx, opts.ProjectID, map[string]any{
		"event": "source_indexed", "source": f.RelPath, "hash": out.sourceHash,
		"chunks": out.chunks, "summarized": out.summarized, "embedded": out.embedded,
	})
	return out, nil
}

// pruneUnshared deletes a source's derived rows only when no other manifest
// row in the project references the same content hash. Identical files share
// content-derived chunk ids, so pruning on behalf of one path must not strip
// chunks another path still needs (ADR-0028 §5).
func (ix *Indexer) pruneUnshared(ctx context.Context, projectID, sourceHash string) error {
	if sourceHash == "" {
		return nil
	}
	n, err := ix.manifest.CountHash(ctx, projectID, sourceHash)
	if err != nil {
		return err
	}
	if n > 1 {
		return nil
	}
	_, err = ix.chunks.PruneSource(ctx, sourceHash)
	return err
}

// embedSource embeds a digest of every summarized chunk of a source. Chunks
// whose stored digest is unchanged are skipped, so a re-pass makes no model
// call for them.
func (ix *Indexer) embedSource(ctx context.Context, sourceHash string, opts IndexOptions) (int, error) {
	if !ix.vectorEnabled() {
		return 0, nil
	}
	entries, err := ix.chunks.IndexForSource(ctx, sourceHash)
	if err != nil {
		return 0, err
	}
	embedBytes := opts.EmbedBytes
	if embedBytes < 0 {
		embedBytes = 0
	}
	var ids, texts, hashes []string
	for _, e := range entries {
		if e.SummaryStatus != "ready" {
			continue
		}
		rec, err := ix.chunks.Get(ctx, e.ChunkID)
		if err != nil {
			return 0, err
		}
		text := embeddingDigest(e, rec.Content, embedBytes)
		h := hashBytes([]byte(text))
		cur, err := ix.embeddingCurrent(ctx, e.ChunkID, h)
		if err != nil {
			return 0, err
		}
		if cur {
			continue
		}
		ids = append(ids, e.ChunkID)
		texts = append(texts, text)
		hashes = append(hashes, h)
	}
	if len(texts) == 0 {
		return 0, nil
	}
	vecs, err := ix.embed.Embed(ctx, texts)
	if err != nil {
		return 0, err
	}
	if len(vecs) != len(texts) {
		return 0, fmt.Errorf("mce: embedder returned %d vectors for %d texts", len(vecs), len(texts))
	}
	for i := range ids {
		if err := ix.vectors.Put(ctx, EmbeddingRecord{
			OwnerID: ids[i], Kind: SourceChunk, Model: ix.embed.Model(),
			Vector: vecs[i], ContentHash: hashes[i],
			Scope: Scope{ProjectID: opts.ProjectID},
		}); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

// embeddingCurrent reports whether ownerID already carries an embedding for
// the current model with the given digest hash.
func (ix *Indexer) embeddingCurrent(ctx context.Context, ownerID, digestHash string) (bool, error) {
	var got string
	err := ix.chunks.db.DB().QueryRowContext(ctx,
		`SELECT content_hash FROM memory_embeddings WHERE owner_id = ? AND model = ?`,
		ownerID, ix.embed.Model()).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mce: read embedding state: %w", err)
	}
	return got == digestHash, nil
}

// embeddingDigest is the deterministic text embedded for a chunk (ADR-0028
// §4): path, line range, summary, and a bounded content prefix.
func embeddingDigest(e IndexEntry, content []byte, maxBytes int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s L%d-%d ", e.SourceRelPath, e.LineStart, e.LineEnd)
	if e.Summary != "" {
		b.WriteString(e.Summary)
		b.WriteByte('\n')
	}
	if maxBytes > 0 && len(content) > 0 {
		c := content
		if len(c) > maxBytes {
			c = c[:maxBytes]
		}
		b.Write(c)
	}
	return b.String()
}

// audit appends a `context` event under the indexing surface.
func (ix *Indexer) audit(ctx context.Context, projectID string, data map[string]any) {
	_, _ = ix.chunks.db.AppendEvent(ctx, store.Event{Category: "context", ProjectID: projectID, Data: data})
}
