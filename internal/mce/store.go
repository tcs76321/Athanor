// Package mce persists the Multidimensional Context Engine's dormant chunks
// and Dormant Index (ARCHITECTURE §10.1; ROADMAP M5-T2; ADR-0021).
//
// It is the storage half of the MCE: it wraps the §10.1 division engine
// (internal/mce/division) with a SQLite-backed chunk store, so a divided
// source can be persisted, listed as a Dormant Index, and reassembled
// byte-for-byte. The MCE never re-derives content: what division produced is
// what reassembly returns.
//
// The package deliberately does NOT import internal/llm (ADR-0021 §2). The
// Dormant Index summary is produced through the Summarizer interface, whose
// LLM-backed adapter lives in cmd/. This preserves Gate G2 rule 1's intent:
// internalapi can dispatch into the MCE without gaining a model-call path.
package mce

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/mce/division"
	"github.com/tcs76321/athanor/internal/store"
)

// ErrNotFound reports an unknown chunk or source hash.
var ErrNotFound = errors.New("mce: not found")

// ContentMismatchError reports that a chunk's stored bytes no longer match
// their recorded SHA-256, or that a source's bytes do not match the hash it
// is being stored under. Failing loudly beats serving bitrot (the same
// posture as artifact.ContentMismatchError).
type ContentMismatchError struct {
	ID       string
	WantHash string
	GotHash  string
}

func (e *ContentMismatchError) Error() string {
	return fmt.Sprintf("mce: chunk %s hash mismatch: recorded %s, actual %s", e.ID, e.WantHash, e.GotHash)
}

// SourceRef attributes a divided source to an optional project and job.
type SourceRef struct {
	// RelPath is the workspace-relative path the chunks were divided from.
	RelPath string
	// ProjectID and JobID are optional attribution (§9.2) used by the
	// Dormant Index to scope chunks to a project or execution.
	ProjectID string
	JobID     string
}

// ChunkRecord is one persisted dormant chunk (§10.1).
type ChunkRecord struct {
	ID            string
	SourceRelPath string
	SourceHash    string
	Lang          string
	Kind          division.Kind
	ByteStart     int
	ByteEnd       int
	LineStart     int
	LineEnd       int
	ContentHash   string
	Content       []byte
	ProjectID     string
	JobID         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// IndexEntry is one Dormant Index row (§10.1): the table of contents the
// prompt publishes so the model can request a context_swap by chunk ID.
type IndexEntry struct {
	ChunkID       string
	SourceRelPath string
	Lang          string
	Kind          division.Kind
	ByteStart     int
	ByteEnd       int
	LineStart     int
	LineEnd       int
	Summary       string
	SummaryStatus string
}

// ChunkStore persists dormant chunks and their Dormant Index rows.
type ChunkStore struct {
	db *store.Store
}

// NewChunkStore returns a ChunkStore over the daemon's single database.
func NewChunkStore(s *store.Store) *ChunkStore { return &ChunkStore{db: s} }

// hashBytes returns the lowercase hex SHA-256 of b.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// nullIfEmpty maps Go empty strings to SQL NULL for optional columns.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SourceHash returns the SHA-256 (hex) of a whole source. Callers compute it
// once and pass it to VerifySourceHash; the division engine embeds the same
// hash on every chunk it produces.
func SourceHash(src []byte) string { return hashBytes(src) }

// VerifySourceHash reports whether src hashes to wantHash. A mismatch is a
// ContentMismatchError, so a caller re-ingesting a changed file can branch on
// it with errors.As.
func VerifySourceHash(wantHash string, src []byte) error {
	got := hashBytes(src)
	if got != wantHash {
		return &ContentMismatchError{ID: "source", WantHash: wantHash, GotHash: got}
	}
	return nil
}

const upsertChunkSQL = `
INSERT INTO context_chunks
    (id, source_relpath, source_hash, lang, kind,
     byte_start, byte_end, line_start, line_end,
     content_hash, content, project_id, job_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    source_relpath = excluded.source_relpath,
    content        = excluded.content,
    content_hash   = excluded.content_hash`

const upsertIndexSQL = `
INSERT INTO dormant_index (chunk_id) VALUES (?)
ON CONFLICT(chunk_id) DO NOTHING`

// PutSource persists every chunk of one divided source in a single
// transaction, creating a pending Dormant Index row per chunk. It is
// idempotent: chunk IDs are deterministic (ADR-0021 §5), so re-ingesting an
// unchanged source updates the same rows and preserves any existing summary.
// It returns the number of chunks written and appends one `context` audit
// event.
func (c *ChunkStore) PutSource(ctx context.Context, ref SourceRef, chunks []division.Chunk) (int, error) {
	if len(chunks) == 0 {
		return 0, nil
	}
	sourceHash := chunks[0].SourceHash
	if sourceHash == "" {
		return 0, errors.New("mce: chunks carry no source hash (division must set SourceHash)")
	}
	for _, ch := range chunks {
		if ch.ID == "" {
			return 0, errors.New("mce: chunk has no ID (division must set it)")
		}
		if ch.SourceHash != sourceHash {
			return 0, errors.New("mce: PutSource called with chunks from different sources")
		}
	}
	relPath := ref.RelPath
	if relPath == "" {
		relPath = chunks[0].FilePath
	}

	tx, err := c.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("mce: begin put source: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, ch := range chunks {
		if _, err := tx.ExecContext(ctx, upsertChunkSQL,
			ch.ID, relPath, sourceHash, ch.Lang, string(ch.Kind),
			ch.ByteStart, ch.ByteEnd, ch.LineStart, ch.LineEnd,
			hashBytes(ch.Content), ch.Content,
			nullIfEmpty(ref.ProjectID), nullIfEmpty(ref.JobID),
		); err != nil {
			return 0, fmt.Errorf("mce: upsert chunk %s: %w", ch.ID, err)
		}
		if _, err := tx.ExecContext(ctx, upsertIndexSQL, ch.ID); err != nil {
			return 0, fmt.Errorf("mce: ensure dormant index row %s: %w", ch.ID, err)
		}
	}

	data, err := json.Marshal(map[string]any{
		"event": "chunks_stored", "source": relPath, "hash": sourceHash, "chunks": len(chunks),
	})
	if err != nil {
		return 0, fmt.Errorf("mce: marshal put source event: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (category, level, project_id, job_id, data_json)
		 VALUES ('context', 'info', ?, ?, ?)`,
		nullIfEmpty(ref.ProjectID), nullIfEmpty(ref.JobID), string(data),
	); err != nil {
		return 0, fmt.Errorf("mce: append context event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("mce: commit put source: %w", err)
	}
	return len(chunks), nil
}

const chunkSelect = `
SELECT id, source_relpath, source_hash, lang, kind,
       byte_start, byte_end, line_start, line_end,
       content_hash, content,
       COALESCE(project_id, ''), COALESCE(job_id, ''),
       created_at, updated_at
FROM context_chunks`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// scanChunk reads one context_chunks row. It returns sql.ErrNoRows
// unwrapped so callers can match it with errors.Is.
func scanChunk(sc scanner) (ChunkRecord, error) {
	var r ChunkRecord
	var kind, createdAt, updatedAt string
	if err := sc.Scan(
		&r.ID, &r.SourceRelPath, &r.SourceHash, &r.Lang, &kind,
		&r.ByteStart, &r.ByteEnd, &r.LineStart, &r.LineEnd,
		&r.ContentHash, &r.Content,
		&r.ProjectID, &r.JobID,
		&createdAt, &updatedAt,
	); err != nil {
		return ChunkRecord{}, err
	}
	r.Kind = division.Kind(kind)
	var err error
	if r.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return ChunkRecord{}, fmt.Errorf("mce: parse chunk created_at %q: %w", createdAt, err)
	}
	if r.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return ChunkRecord{}, fmt.Errorf("mce: parse chunk updated_at %q: %w", updatedAt, err)
	}
	return r, nil
}

// Get returns one chunk by ID. An unknown ID is ErrNotFound.
func (c *ChunkStore) Get(ctx context.Context, chunkID string) (ChunkRecord, error) {
	row := c.db.DB().QueryRowContext(ctx, chunkSelect+` WHERE id = ?`, chunkID)
	rec, err := scanChunk(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChunkRecord{}, fmt.Errorf("%w: chunk %s", ErrNotFound, chunkID)
	}
	if err != nil {
		return ChunkRecord{}, err
	}
	return rec, nil
}

// ListBySource returns every chunk of a source hash, ordered by byte offset.
// An unknown source hash yields an empty slice (not an error), so callers can
// distinguish "no chunks" from a storage failure.
func (c *ChunkStore) ListBySource(ctx context.Context, sourceHash string) ([]ChunkRecord, error) {
	rows, err := c.db.DB().QueryContext(ctx,
		chunkSelect+` WHERE source_hash = ? ORDER BY byte_start`, sourceHash)
	if err != nil {
		return nil, fmt.Errorf("mce: list chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ChunkRecord
	for rows.Next() {
		rec, err := scanChunk(rows)
		if err != nil {
			return nil, fmt.Errorf("mce: scan chunk: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mce: iterate chunks: %w", err)
	}
	return out, nil
}

const indexSelect = `
SELECT c.id, c.source_relpath, c.lang, c.kind,
       c.byte_start, c.byte_end, c.line_start, c.line_end,
       COALESCE(d.summary, ''), COALESCE(d.summary_status, 'pending')
FROM context_chunks c
LEFT JOIN dormant_index d ON d.chunk_id = c.id`

// IndexForSource returns the Dormant Index (§10.1) for a source hash: the
// metadata-only table of contents (chunk ID, path, line range, kind, one-line
// summary) ordered by byte offset. This is what the prompt publishes.
func (c *ChunkStore) IndexForSource(ctx context.Context, sourceHash string) ([]IndexEntry, error) {
	rows, err := c.db.DB().QueryContext(ctx,
		indexSelect+` WHERE c.source_hash = ? ORDER BY c.byte_start`, sourceHash)
	if err != nil {
		return nil, fmt.Errorf("mce: list dormant index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanIndex(rows)
}

// IndexForJob returns the Dormant Index (§10.1) for one job's chunks: the
// per-job table of contents the M5-T5 assembler publishes in §11.2 §10.
// Chunks are attributed to a job at ingestion (SourceRef.JobID), so a job
// that divided several sources sees them all, ordered by source path and
// then byte offset. An unknown job yields an empty slice (not an error),
// exactly like IndexForSource.
//
// An empty jobID returns nothing rather than every un-attributed row: rows
// with no job belong to no job's context, and publishing them would leak
// another scope's chunks into this prompt.
func (c *ChunkStore) IndexForJob(ctx context.Context, jobID string) ([]IndexEntry, error) {
	if jobID == "" {
		return nil, nil
	}
	rows, err := c.db.DB().QueryContext(ctx,
		indexSelect+` WHERE c.job_id = ? ORDER BY c.source_relpath, c.byte_start`, jobID)
	if err != nil {
		return nil, fmt.Errorf("mce: list dormant index for job: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanIndex(rows)
}

// DefaultRepositoryIndexLimit bounds the project-scoped Dormant Index rows a
// single prompt publishes when the caller passes no explicit limit.
const DefaultRepositoryIndexLimit = 20

// IndexForProject returns the Dormant Index (§10.1) rows for a project's
// repository chunks (F5, ADR-0059): chunks the repository indexer ingested,
// which carry a project_id and no job_id. When query has full-text tokens the
// rows are ranked by FTS5 bm25 over the Dormant Index summaries; otherwise the
// most recently updated rows are returned. limit <= 0 selects
// DefaultRepositoryIndexLimit.
//
// This is the read the engine's tier-6 Dormant Index uses to surface project
// context to a normal job: the model sees the ranked table of contents and can
// context_swap any entry (ChunkStore.Owns admits project-owned chunks). The
// §10.5 ladder still evicts tier 6 first, so an over-large index cannot breach
// the context budget.
func (c *ChunkStore) IndexForProject(ctx context.Context, projectID, query string, limit int) ([]IndexEntry, error) {
	if projectID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = DefaultRepositoryIndexLimit
	}
	if match := ftsQuery(query); match != "" {
		rows, err := c.db.DB().QueryContext(ctx, `
			SELECT c.id, c.source_relpath, c.lang, c.kind,
			       c.byte_start, c.byte_end, c.line_start, c.line_end,
			       COALESCE(d.summary, ''), COALESCE(d.summary_status, 'pending')
			FROM dormant_index_fts
			JOIN dormant_index d ON d.rowid = dormant_index_fts.rowid
			JOIN context_chunks c ON c.id = d.chunk_id
			WHERE dormant_index_fts MATCH ?
			  AND c.project_id = ?
			  AND c.job_id IS NULL
			ORDER BY bm25(dormant_index_fts), c.source_relpath, c.byte_start
			LIMIT ?`, match, projectID, limit)
		if err != nil {
			return nil, fmt.Errorf("mce: rank project dormant index: %w", err)
		}
		defer func() { _ = rows.Close() }()
		return scanIndex(rows)
	}
	rows, err := c.db.DB().QueryContext(ctx,
		indexSelect+` WHERE c.project_id = ? AND c.job_id IS NULL
			ORDER BY c.updated_at DESC, c.source_relpath, c.byte_start
			LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("mce: list project dormant index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanIndex(rows)
}

// scanIndex materializes Dormant Index rows from an `indexSelect` query.
func scanIndex(rows *sql.Rows) ([]IndexEntry, error) {
	var out []IndexEntry
	for rows.Next() {
		var e IndexEntry
		var kind string
		if err := rows.Scan(
			&e.ChunkID, &e.SourceRelPath, &e.Lang, &kind,
			&e.ByteStart, &e.ByteEnd, &e.LineStart, &e.LineEnd,
			&e.Summary, &e.SummaryStatus,
		); err != nil {
			return nil, fmt.Errorf("mce: scan dormant index: %w", err)
		}
		e.Kind = division.Kind(kind)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mce: iterate dormant index: %w", err)
	}
	return out, nil
}

// Reassemble loads every chunk of a source hash and concatenates them in byte
// order, verifying exact tiling and per-chunk content hashes. The result is
// byte-identical to the source that was divided (§10.1 P1). An unknown source
// hash is ErrNotFound.
func (c *ChunkStore) Reassemble(ctx context.Context, sourceHash string) ([]byte, error) {
	recs, err := c.ListBySource(ctx, sourceHash)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("%w: source %s", ErrNotFound, sourceHash)
	}
	return concat(recs)
}

// concat joins chunk contents in order, refusing a non-contiguous set and
// refusing any chunk whose bytes no longer match its recorded hash.
func concat(recs []ChunkRecord) ([]byte, error) {
	var buf bytes.Buffer
	next := 0
	for _, r := range recs {
		if r.ByteStart != next {
			return nil, fmt.Errorf("mce: source %s not contiguous at chunk %s (byte %d, want %d)",
				r.SourceHash, r.ID, r.ByteStart, next)
		}
		if got := hashBytes(r.Content); got != r.ContentHash {
			return nil, &ContentMismatchError{ID: r.ID, WantHash: r.ContentHash, GotHash: got}
		}
		buf.Write(r.Content)
		next = r.ByteEnd
	}
	return buf.Bytes(), nil
}
