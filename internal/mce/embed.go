package mce

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/tcs76321/athanor/internal/store"
)

// Memory retrieval: embeddings and the vector index (ROADMAP M5-T7;
// ADR-0026 §2–§3).
//
// §25's query_memory is a hybrid — full-text (FTS5) plus vector
// similarity. This file owns the vector half. The in-tree VectorIndex is a
// brute-force cosine over embeddings stored as BLOBs in `memory_embeddings`
// (migration 0013). The sqlite-vec `vec0` accelerator is the deferred
// alternative implementation of the same interface, and that is where the
// task-000 connection-affinity work belongs: load the extension once on the
// daemon's single pooled connection (ADR-0003) and release it immediately.

// SourceKind selects which memory store owns an entry: a §10.3 compaction
// memo (`compacted_memory`) or a dormant chunk (`context_chunks`).
type SourceKind string

const (
	SourceMemo  SourceKind = "memo"
	SourceChunk SourceKind = "chunk"
)

// Scope restricts a retrieval to a project and/or a job (§9.2). An empty
// Scope matches nothing — publishing another scope's memory into a prompt
// would be a containment leak (ADR-0026 §5).
type Scope struct {
	ProjectID string
	JobID     string
}

// Empty reports whether the scope carries no restriction. Such a scope is
// rejected rather than run: an unbounded query would leak every project's
// memory into one prompt.
func (s Scope) Empty() bool { return s.ProjectID == "" && s.JobID == "" }

// ErrEmbeddingDimMismatch reports that a vector's length disagrees with the
// dimension recorded for its model. A mismatch is fail-loud: switching the
// embedding model requires a re-index, and silently truncating or padding
// would corrupt ranking (ADR-0026 §3).
var ErrEmbeddingDimMismatch = errors.New("mce: embedding dimension mismatch")

// Embedder turns text into vectors. The LLM-backed adapter lives in cmd/
// (`internal/mce` must not import `internal/llm` — ADR-0021 §2).
type Embedder interface {
	// Embed returns one vector per input text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Model names the embedding model, so vectors can be keyed by model
	// and cross-model comparisons are impossible.
	Model() string
}

// EmbeddingRecord is one vector to persist.
type EmbeddingRecord struct {
	OwnerID     string
	Kind        SourceKind
	Model       string
	Vector      []float32
	ContentHash string
	Scope       Scope
}

// VectorHit is one scored owner returned by a nearest-neighbour search.
type VectorHit struct {
	OwnerID string
	Score   float64 // cosine similarity, higher is closer
}

// VectorQuery is one nearest-neighbour request.
type VectorQuery struct {
	Query []float32
	Model string
	Kind  SourceKind
	Scope Scope
	K     int
}

// VectorIndex stores embeddings and answers nearest-neighbour queries. The
// in-tree implementation is SQLVectorIndex; a future sqlite-vec `vec0`
// implementation replaces it behind this interface (ADR-0026 §2).
type VectorIndex interface {
	Put(ctx context.Context, rec EmbeddingRecord) error
	Nearest(ctx context.Context, q VectorQuery) ([]VectorHit, error)
}

// SQLVectorIndex is the in-tree VectorIndex: brute-force cosine over the
// BLOBs in `memory_embeddings`. O(n) per query, which is fine at local
// scale — and precisely why the native accelerator is deferred rather than
// blocked on.
type SQLVectorIndex struct {
	db *store.Store
}

// NewSQLVectorIndex returns a vector index over the daemon's database.
func NewSQLVectorIndex(s *store.Store) *SQLVectorIndex { return &SQLVectorIndex{db: s} }

// Put upserts one embedding. Re-putting the same (owner, model) replaces
// the stored vector; `content_hash` is recorded so a caller can tell that
// the owner's bytes changed. The dimension is pinned by the rows already
// stored for a model: a later vector of a different length is
// ErrEmbeddingDimMismatch, never a silent overwrite.
func (v *SQLVectorIndex) Put(ctx context.Context, rec EmbeddingRecord) error {
	if rec.OwnerID == "" || rec.Model == "" {
		return errors.New("mce: embedding requires owner_id and model")
	}
	if rec.Kind != SourceMemo && rec.Kind != SourceChunk {
		return fmt.Errorf("mce: embedding has unknown source kind %q", rec.Kind)
	}
	if len(rec.Vector) == 0 {
		return errors.New("mce: embedding has an empty vector")
	}
	var dim int
	err := v.db.DB().QueryRowContext(ctx,
		`SELECT dim FROM memory_embeddings WHERE model = ? LIMIT 1`, rec.Model).Scan(&dim)
	switch {
	case err == nil && dim != len(rec.Vector):
		return fmt.Errorf("%w: model %s stores dim %d, got %d",
			ErrEmbeddingDimMismatch, rec.Model, dim, len(rec.Vector))
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("mce: reading embedding dimension: %w", err)
	}

	if _, err := v.db.DB().ExecContext(ctx, `
		INSERT INTO memory_embeddings
		    (owner_id, source_kind, model, dim, vector, content_hash, project_id, job_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_id, model) DO UPDATE SET
		    dim = excluded.dim,
		    vector = excluded.vector,
		    content_hash = excluded.content_hash,
		    project_id = excluded.project_id,
		    job_id = excluded.job_id`,
		rec.OwnerID, string(rec.Kind), rec.Model, len(rec.Vector),
		encodeVector(rec.Vector), rec.ContentHash,
		nullIfEmpty(rec.Scope.ProjectID), nullIfEmpty(rec.Scope.JobID)); err != nil {
		return fmt.Errorf("mce: store embedding for %s: %w", rec.OwnerID, err)
	}
	return nil
}

// Nearest returns the k owners of the given kind and scope closest to the
// query vector by cosine similarity, best first. Ties break on owner id so
// the order is deterministic. An empty scope or an empty query returns
// nothing rather than scanning every row.
func (v *SQLVectorIndex) Nearest(ctx context.Context, q VectorQuery) ([]VectorHit, error) {
	if q.Scope.Empty() || len(q.Query) == 0 || q.Model == "" {
		return nil, nil
	}
	rows, err := v.db.DB().QueryContext(ctx, `
		SELECT owner_id, dim, vector FROM memory_embeddings
		WHERE model = ? AND source_kind = ?
		  AND (? = '' OR project_id = ?)
		  AND (? = '' OR job_id = ?)`,
		q.Model, string(q.Kind),
		q.Scope.ProjectID, q.Scope.ProjectID,
		q.Scope.JobID, q.Scope.JobID)
	if err != nil {
		return nil, fmt.Errorf("mce: scan embeddings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []VectorHit
	for rows.Next() {
		var ownerID string
		var dim int
		var blob []byte
		if err := rows.Scan(&ownerID, &dim, &blob); err != nil {
			return nil, fmt.Errorf("mce: scan embedding row: %w", err)
		}
		if dim != len(q.Query) {
			return nil, fmt.Errorf("%w: model %s stores dim %d, query is %d",
				ErrEmbeddingDimMismatch, q.Model, dim, len(q.Query))
		}
		vec, err := decodeVector(blob, dim)
		if err != nil {
			return nil, err
		}
		score, err := Cosine(q.Query, vec)
		if err != nil {
			return nil, err
		}
		hits = append(hits, VectorHit{OwnerID: ownerID, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mce: iterate embeddings: %w", err)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].OwnerID < hits[j].OwnerID
	})
	if q.K > 0 && len(hits) > q.K {
		hits = hits[:q.K]
	}
	return hits, nil
}

// Cosine returns the cosine similarity of two equal-length vectors. A
// zero-length or all-zero operand yields 0 rather than a division by zero.
func Cosine(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d vs %d", ErrEmbeddingDimMismatch, len(a), len(b))
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), nil
}

// encodeVector serialises a vector as little-endian float32 bytes.
func encodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// decodeVector reverses encodeVector. dim is the expected length, checked
// against the BLOB so a corrupted row fails loudly.
func decodeVector(b []byte, dim int) ([]float32, error) {
	if len(b) != 4*dim {
		return nil, fmt.Errorf("mce: embedding blob is %d bytes, want %d for dim %d", len(b), 4*dim, dim)
	}
	v := make([]float32, dim)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v, nil
}
