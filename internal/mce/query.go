package mce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/store"
)

// Memory retrieval: the hybrid query_memory engine (ROADMAP M5-T7;
// ADR-0026).
//
// This file owns the query side: full-text search over the FTS5 index
// (migration 0013), optional vector search through a VectorIndex, and the
// reciprocal-rank fusion that combines them. Chunk bodies are never
// returned — a hit names the chunk id so the caller can `context_swap` it
// in (ADR-0026 §4). Scope is mandatory: an empty Scope returns nothing
// (ADR-0026 §5).

// DefaultTopK bounds a query when the caller asks for no specific k.
const DefaultTopK = 8

// rrfK is the reciprocal-rank-fusion constant. 60 is the value from the
// original RRF paper; it is deliberately large so the top ranks do not
// dominate the fused score.
const rrfK = 60

// MemoryHit is one retrieved memory entry.
type MemoryHit struct {
	// ID is the owner id: a compacted_memory id (memo) or a chunk id.
	ID   string
	Kind SourceKind
	// Score is the fused reciprocal-rank-fusion score (higher is better).
	Score float64
	// BM25Rank and CosineRank are the 1-based ranks each signal gave this
	// hit; 0 means that signal did not return it.
	BM25Rank   int
	CosineRank int
	// SourceRelPath, Summary, LineStart, LineEnd are set for chunk hits
	// (the Dormant Index entry the caller may context_swap to).
	SourceRelPath string
	Summary       string
	LineStart     int
	LineEnd       int
	// Content is the memo text for memo hits; empty for chunks, whose
	// bytes are BLOBs reached through context_swap.
	Content string
}

// QueryOptions is one memory query. TopK ≤ 0 means DefaultTopK.
type QueryOptions struct {
	Query string
	Scope Scope
	TopK  int
}

// Retriever answers query_memory over the MCE stores.
type Retriever struct {
	db *store.Store
	// embed and vectors are optional: when either is nil, or the embedder
	// reports no model, retrieval is FTS-only (ADR-0026 §3).
	embed   Embedder
	vectors VectorIndex
}

// NewRetriever returns an FTS-only retriever over the daemon's database.
func NewRetriever(s *store.Store) *Retriever { return &Retriever{db: s} }

// WithVector enables the vector half. A nil embedder or index (or an empty
// model name) leaves the retriever FTS-only, which is the shipped default.
func (r *Retriever) WithVector(embed Embedder, vectors VectorIndex) *Retriever {
	r.embed, r.vectors = embed, vectors
	return r
}

// Query runs a hybrid retrieval. The full-text half always runs; the vector
// half runs when an Embedder and VectorIndex are configured, and a failure
// there fails the query loudly rather than silently degrading (the caller's
// envelope is intact; a broken embedder is an operator problem, not a
// reason to serve half an answer).
func (r *Retriever) Query(ctx context.Context, opts QueryOptions) ([]MemoryHit, error) {
	if opts.Scope.Empty() {
		return nil, nil
	}
	k := opts.TopK
	if k <= 0 {
		k = DefaultTopK
	}

	var memoBM25, chunkBM25 []string
	if match := ftsQuery(opts.Query); match != "" {
		var err error
		if memoBM25, err = r.searchMemos(ctx, match, opts.Scope, k); err != nil {
			return nil, err
		}
		if chunkBM25, err = r.searchChunks(ctx, match, opts.Scope, k); err != nil {
			return nil, err
		}
	}

	var memoVec, chunkVec []string
	if r.VectorEnabled() {
		qv, err := r.embedOne(ctx, opts.Query)
		if err != nil {
			return nil, fmt.Errorf("mce: embedding the query: %w", err)
		}
		if memoVec, err = r.nearest(ctx, qv, SourceMemo, opts.Scope, k); err != nil {
			return nil, err
		}
		if chunkVec, err = r.nearest(ctx, qv, SourceChunk, opts.Scope, k); err != nil {
			return nil, err
		}
	}

	memoScores := FuseRRF(memoBM25, memoVec)
	chunkScores := FuseRRF(chunkBM25, chunkVec)

	hits := make([]MemoryHit, 0, len(memoScores)+len(chunkScores))
	for id, score := range memoScores {
		hits = append(hits, MemoryHit{
			ID: id, Kind: SourceMemo, Score: score,
			BM25Rank: rankOf(memoBM25, id), CosineRank: rankOf(memoVec, id),
		})
	}
	for id, score := range chunkScores {
		hits = append(hits, MemoryHit{
			ID: id, Kind: SourceChunk, Score: score,
			BM25Rank: rankOf(chunkBM25, id), CosineRank: rankOf(chunkVec, id),
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	if err := r.hydrate(ctx, hits); err != nil {
		return nil, err
	}
	return hits, nil
}

// FuseRRF returns the reciprocal-rank-fusion score of every id across the
// given ranked lists (best-first, 1-based rank): Σ 1/(rrfK + rank). RRF
// needs no score normalisation — which matters because BM25 and cosine are
// not on comparable scales — and degrades gracefully when one list is empty
// (the FTS-only default). It is exported because it *is* the acceptance
// criterion's "combined BM25 + vector results returned with scores", and a
// pure function is the cheapest place to pin that.
func FuseRRF(lists ...[]string) map[string]float64 {
	scores := make(map[string]float64)
	for _, list := range lists {
		for i, id := range list {
			scores[id] += 1.0 / float64(rrfK+i+1)
		}
	}
	return scores
}

// rankOf returns the 1-based rank of id in a best-first list, or 0.
func rankOf(list []string, id string) int {
	for i, x := range list {
		if x == id {
			return i + 1
		}
	}
	return 0
}

// ftsQuery converts free-form user text into an FTS5 MATCH expression. Only
// [A-Za-z0-9_]+ tokens survive, each quoted as a phrase and OR-ed together,
// so FTS5 operators in the input (`*`, `-`, `NEAR`, quotes) cannot change
// the expression's meaning or raise a syntax error. An input with no tokens
// yields "" (no full-text match).
func ftsQuery(s string) string {
	var terms []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			terms = append(terms, `"`+b.String()+`"`)
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return strings.Join(terms, " OR ")
}

// searchMemos returns the k memo ids best matching match, scoped.
func (r *Retriever) searchMemos(ctx context.Context, match string, scope Scope, k int) ([]string, error) {
	rows, err := r.db.DB().QueryContext(ctx, `
		SELECT m.id
		FROM compacted_memory_fts
		JOIN compacted_memory m ON m.rowid = compacted_memory_fts.rowid
		WHERE compacted_memory_fts MATCH ?
		  AND (? = '' OR m.project_id = ?)
		  AND (? = '' OR m.job_id = ?)
		ORDER BY bm25(compacted_memory_fts)
		LIMIT ?`,
		match, scope.ProjectID, scope.ProjectID, scope.JobID, scope.JobID, k)
	if err != nil {
		return nil, fmt.Errorf("mce: search memos: %w", err)
	}
	return scanIDs(rows, "memos")
}

// searchChunks returns the k chunk ids best matching match, scoped.
func (r *Retriever) searchChunks(ctx context.Context, match string, scope Scope, k int) ([]string, error) {
	rows, err := r.db.DB().QueryContext(ctx, `
		SELECT c.id
		FROM dormant_index_fts
		JOIN dormant_index d ON d.rowid = dormant_index_fts.rowid
		JOIN context_chunks c ON c.id = d.chunk_id
		WHERE dormant_index_fts MATCH ?
		  AND (? = '' OR c.project_id = ?)
		  AND (? = '' OR c.job_id = ?)
		ORDER BY bm25(dormant_index_fts)
		LIMIT ?`,
		match, scope.ProjectID, scope.ProjectID, scope.JobID, scope.JobID, k)
	if err != nil {
		return nil, fmt.Errorf("mce: search chunks: %w", err)
	}
	return scanIDs(rows, "chunks")
}

// scanIDs materialises a single-column id query.
func scanIDs(rows *sql.Rows, what string) ([]string, error) {
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("mce: scan %s: %w", what, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mce: iterate %s: %w", what, err)
	}
	return out, nil
}

// VectorEnabled reports whether the vector half is configured (ADR-0026 §3).
// It is exported so the tool response can tell the caller whether the vector
// signal actually ran, rather than leaving the caller to infer it.
func (r *Retriever) VectorEnabled() bool {
	return r.embed != nil && r.vectors != nil && r.embed.Model() != ""
}

// embedOne embeds a single text and returns its vector.
func (r *Retriever) embedOne(ctx context.Context, text string) ([]float32, error) {
	vs, err := r.embed.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vs) != 1 {
		return nil, fmt.Errorf("mce: embedder returned %d vectors for 1 text", len(vs))
	}
	return vs[0], nil
}

// nearest returns the ordered owner ids the vector index ranks closest.
func (r *Retriever) nearest(ctx context.Context, qv []float32, kind SourceKind, scope Scope, k int) ([]string, error) {
	hits, err := r.vectors.Nearest(ctx, VectorQuery{
		Query: qv, Model: r.embed.Model(), Kind: kind, Scope: scope, K: k,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.OwnerID)
	}
	return out, nil
}

// hydrate fills each hit's display fields from its owner row: memo text from
// compacted_memory, or the Dormant Index summary plus the chunk's path and
// line range. A hit whose owner row vanished (pruned between the index and
// this read) is left bare rather than dropped — its id is still a valid
// handle.
func (r *Retriever) hydrate(ctx context.Context, hits []MemoryHit) error {
	for i := range hits {
		switch hits[i].Kind {
		case SourceMemo:
			var content string
			err := r.db.DB().QueryRowContext(ctx,
				`SELECT content FROM compacted_memory WHERE id = ?`, hits[i].ID).Scan(&content)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("mce: hydrate memo %s: %w", hits[i].ID, err)
			}
			hits[i].Content = content
		case SourceChunk:
			var summary, relpath string
			var lineStart, lineEnd int
			err := r.db.DB().QueryRowContext(ctx, `
				SELECT d.summary, c.source_relpath, c.line_start, c.line_end
				FROM context_chunks c
				JOIN dormant_index d ON d.chunk_id = c.id
				WHERE c.id = ?`, hits[i].ID).Scan(&summary, &relpath, &lineStart, &lineEnd)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("mce: hydrate chunk %s: %w", hits[i].ID, err)
			}
			hits[i].Summary = summary
			hits[i].SourceRelPath = relpath
			hits[i].LineStart = lineStart
			hits[i].LineEnd = lineEnd
		}
	}
	return nil
}
