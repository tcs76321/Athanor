package mce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/store"
)

// Repository indexing manifest (ROADMAP M5-T8; ADR-0028 §2).
//
// `indexed_sources` (migration 0014) records what the M5-T8 indexer has seen:
// one row per (project, workspace-relative path) carrying the file's size,
// mtime, and content hash plus the chunk count it produced. A pass skips a
// file whose size and mtime match its row without reading it; a changed hash
// means the superseded source is pruned and the row is rewritten.

// SourceRow is one manifest row.
type SourceRow struct {
	ProjectID  string
	RelPath    string
	Lang       string
	Size       int64
	MTimeUnix  int64
	SourceHash string
	Chunks     int
	// Status is "ok" after a successful index, or "failed" with Error set;
	// a failed row is retried by a later pass rather than lost.
	Status string
	Error  string
}

// IndexManifest persists the incremental indexing state.
type IndexManifest struct {
	db *store.Store
}

// NewIndexManifest returns a manifest over the daemon's single database.
func NewIndexManifest(s *store.Store) *IndexManifest { return &IndexManifest{db: s} }

const manifestSelect = `
SELECT project_id, relpath, lang, size, mtime_unix, source_hash, chunks, status, COALESCE(error, '')
FROM indexed_sources`

// scanSource reads one indexed_sources row.
func scanSource(sc scanner) (SourceRow, error) {
	var r SourceRow
	if err := sc.Scan(
		&r.ProjectID, &r.RelPath, &r.Lang, &r.Size, &r.MTimeUnix,
		&r.SourceHash, &r.Chunks, &r.Status, &r.Error,
	); err != nil {
		return SourceRow{}, err
	}
	return r, nil
}

// Get returns the manifest row for (projectID, relpath). ok is false when the
// path has never been indexed.
func (m *IndexManifest) Get(ctx context.Context, projectID, relpath string) (SourceRow, bool, error) {
	row := m.db.DB().QueryRowContext(ctx,
		manifestSelect+` WHERE project_id = ? AND relpath = ?`, projectID, relpath)
	rec, err := scanSource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceRow{}, false, nil
	}
	if err != nil {
		return SourceRow{}, false, fmt.Errorf("mce: read manifest %s/%s: %w", projectID, relpath, err)
	}
	return rec, true, nil
}

// List returns every manifest row for a project, ordered by path. An unknown
// project yields an empty slice (not an error).
func (m *IndexManifest) List(ctx context.Context, projectID string) ([]SourceRow, error) {
	rows, err := m.db.DB().QueryContext(ctx,
		manifestSelect+` WHERE project_id = ? ORDER BY relpath`, projectID)
	if err != nil {
		return nil, fmt.Errorf("mce: list manifest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SourceRow
	for rows.Next() {
		rec, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("mce: scan manifest: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mce: iterate manifest: %w", err)
	}
	return out, nil
}

// Put upserts one manifest row. An empty Status is stored as "ok".
func (m *IndexManifest) Put(ctx context.Context, row SourceRow) error {
	if row.ProjectID == "" || row.RelPath == "" {
		return errors.New("mce: manifest row requires project_id and relpath")
	}
	status := row.Status
	if status == "" {
		status = "ok"
	}
	if _, err := m.db.DB().ExecContext(ctx, `
		INSERT INTO indexed_sources
		    (project_id, relpath, lang, size, mtime_unix, source_hash, chunks, status, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, relpath) DO UPDATE SET
		    lang        = excluded.lang,
		    size        = excluded.size,
		    mtime_unix  = excluded.mtime_unix,
		    source_hash = excluded.source_hash,
		    chunks      = excluded.chunks,
		    status      = excluded.status,
		    error       = excluded.error,
		    indexed_at  = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		row.ProjectID, row.RelPath, row.Lang, row.Size, row.MTimeUnix,
		row.SourceHash, row.Chunks, status, nullIfEmpty(row.Error)); err != nil {
		return fmt.Errorf("mce: put manifest %s/%s: %w", row.ProjectID, row.RelPath, err)
	}
	return nil
}

// Delete removes a manifest row. Deleting an absent row is a no-op.
func (m *IndexManifest) Delete(ctx context.Context, projectID, relpath string) error {
	if _, err := m.db.DB().ExecContext(ctx,
		`DELETE FROM indexed_sources WHERE project_id = ? AND relpath = ?`,
		projectID, relpath); err != nil {
		return fmt.Errorf("mce: delete manifest %s/%s: %w", projectID, relpath, err)
	}
	return nil
}
