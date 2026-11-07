// Package store: SQLite-backed repository for quarantined files
// (ROADMAP M4-T2; migration 0008; ADR-0015). The repo is the
// only writer of quarantined_files; the ingress pipeline
// (internal/airlock/ingress) and the egress pipeline
// (internal/airlock/egress) go through Repo.
//
// Idempotency: Put is keyed on the SHA-256 primary key, so a
// second Put with the same content is a no-op (no error, no
// row rewrite). The ingress pipeline relies on this for the
// "duplicate_ignored" path; the egress pipeline relies on it
// for the "already exported" path.
//
// Append-only contract: Put writes the quarantine row and the
// corresponding audit `events` row in one transaction. After
// Put returns successfully, the row is durable and the audit
// trail records the event. There is no Update or Delete path;
// a quarantined file is a permanent record. A future
// "release from quarantine" feature is a separate schema
// change, not a CRUD operation on this table.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrQuarantineNotFound reports a SHA-256 that has no quarantine
// row. Callers use it to distinguish "file was never
// quarantined" from "no such column" / "DB error" — both of
// which would surface as different error types.
var ErrQuarantineNotFound = errors.New("quarantine: file not found")

// Quarantine is one row of the quarantined_files table. The
// shape mirrors the migration 0008 schema; the JSON `details`
// payload is opaque to SQL but machine-parseable for audit
// queries and human-readable for post-mortems.
type Quarantine struct {
	SHA256     string
	RelPath    string
	Reason     string
	Details    json.RawMessage
	SourceSize int64
	StoredPath string
	IngestedAt time.Time
	Pipeline   string
	JobID      string // empty when not engine-driven
}

// QuarantineRepo persists quarantined files. Construct with
// NewQuarantineRepo; the zero value is unusable.
type QuarantineRepo struct {
	db *Store
}

// NewQuarantineRepo returns a repo writing through db.
func NewQuarantineRepo(db *Store) *QuarantineRepo {
	return &QuarantineRepo{db: db}
}

// Put inserts (or no-ops on duplicate) a quarantine row,
// writing the corresponding `airlock` audit event in the same
// transaction. Idempotent: a row with the same SHA-256 already
// exists returns nil with `existed=true` so callers can record
// a `duplicate_ignored` event without re-writing the row.
//
// The transaction is at the SQLite level: an audit-event
// insert that fails rolls back the quarantine row insert, so
// the two are observed atomically or not at all.
func (r *QuarantineRepo) Put(ctx context.Context, q Quarantine) (existed bool, err error) {
	if q.SHA256 == "" {
		return false, fmt.Errorf("quarantine: SHA256 must be non-empty")
	}
	if q.Pipeline == "" {
		return false, fmt.Errorf("quarantine: pipeline must be non-empty (ingress|egress|user-prompt)")
	}
	switch q.Pipeline {
	case "ingress", "egress", "user-prompt":
	default:
		return false, fmt.Errorf("quarantine: invalid pipeline %q (want ingress|egress|user-prompt)", q.Pipeline)
	}
	if q.IngestedAt.IsZero() {
		q.IngestedAt = time.Now().UTC()
	}
	details := q.Details
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("quarantine: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// INSERT OR IGNORE so a second Put on the same SHA-256
	// is a no-op without a UNIQUE-constraint failure. The
	// RowsAffected tells us whether the row was new.
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO quarantined_files
		 (sha256, relpath, reason, details, source_size,
		  stored_path, ingested_at, pipeline, job_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.SHA256, q.RelPath, q.Reason, string(details),
		q.SourceSize, q.StoredPath,
		q.IngestedAt.UTC().Format(time.RFC3339Nano),
		q.Pipeline, nullIfEmpty(q.JobID),
	)
	if err != nil {
		return false, fmt.Errorf("quarantine: insert: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("quarantine: rows affected: %w", err)
	}
	if n == 0 {
		// Already existed; the audit event is a
		// `duplicate_ignored` (the caller decides the
		// event name; we just don't double-write the
		// quarantine row).
		return true, tx.Commit()
	}
	// Write the audit event in the same transaction.
	data := map[string]any{
		"event":       "quarantined",
		"sha256":      q.SHA256,
		"relpath":     q.RelPath,
		"reason":      q.Reason,
		"source_size": q.SourceSize,
		"stored_path": q.StoredPath,
		"pipeline":    q.Pipeline,
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return false, fmt.Errorf("quarantine: marshal event: %w", err)
	}
	var jobID sql.NullString
	if q.JobID != "" {
		jobID = sql.NullString{String: q.JobID, Valid: true}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (category, level, job_id, data_json)
		 VALUES ('airlock', 'warn', ?, ?)`,
		jobID, string(raw),
	); err != nil {
		return false, fmt.Errorf("quarantine: insert event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("quarantine: commit: %w", err)
	}
	return false, nil
}

// Get returns the quarantine row for sha256, or
// ErrQuarantineNotFound if none exists.
func (r *QuarantineRepo) Get(ctx context.Context, sha256 string) (Quarantine, error) {
	var (
		q          Quarantine
		details    string
		ingestedAt string
		jobID      sql.NullString
	)
	err := r.db.ReadDB().QueryRowContext(ctx,
		`SELECT sha256, relpath, reason, details, source_size,
		        stored_path, ingested_at, pipeline, COALESCE(job_id, '')
		 FROM quarantined_files WHERE sha256 = ?`, sha256,
	).Scan(
		&q.SHA256, &q.RelPath, &q.Reason, &details, &q.SourceSize,
		&q.StoredPath, &ingestedAt, &q.Pipeline, &jobID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Quarantine{}, ErrQuarantineNotFound
		}
		return Quarantine{}, fmt.Errorf("quarantine: get: %w", err)
	}
	q.Details = json.RawMessage(details)
	t, err := time.Parse(time.RFC3339Nano, ingestedAt)
	if err != nil {
		return Quarantine{}, fmt.Errorf("quarantine: parse ingested_at: %w", err)
	}
	q.IngestedAt = t
	if jobID.Valid {
		q.JobID = jobID.String
	}
	return q, nil
}

// List returns quarantine rows newer than `since`, ordered by
// ingested_at ascending. An empty `since` returns all rows.
// `pipeline` filters to a single kind; empty string disables
// the filter. `limit` <= 0 means no limit. Used by the audit
// surface and the (future) quarantine directory sweep.
func (r *QuarantineRepo) List(ctx context.Context, since time.Time, pipeline string, limit int) ([]Quarantine, error) {
	q := `SELECT sha256, relpath, reason, details, source_size,
	             stored_path, ingested_at, pipeline, COALESCE(job_id, '')
	      FROM quarantined_files WHERE 1=1`
	args := []any{}
	if !since.IsZero() {
		q += ` AND ingested_at >= ?`
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	if pipeline != "" {
		q += ` AND pipeline = ?`
		args = append(args, pipeline)
	}
	q += ` ORDER BY ingested_at ASC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.ReadDB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("quarantine: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Quarantine
	for rows.Next() {
		var (
			qu         Quarantine
			details    string
			ingestedAt string
			jobID      sql.NullString
		)
		if err := rows.Scan(
			&qu.SHA256, &qu.RelPath, &qu.Reason, &details, &qu.SourceSize,
			&qu.StoredPath, &ingestedAt, &qu.Pipeline, &jobID,
		); err != nil {
			return nil, fmt.Errorf("quarantine: scan: %w", err)
		}
		qu.Details = json.RawMessage(details)
		t, perr := time.Parse(time.RFC3339Nano, ingestedAt)
		if perr != nil {
			return nil, fmt.Errorf("quarantine: parse ingested_at: %w", perr)
		}
		qu.IngestedAt = t
		if jobID.Valid {
			qu.JobID = jobID.String
		}
		out = append(out, qu)
	}
	return out, rows.Err()
}
