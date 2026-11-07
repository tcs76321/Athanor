// Package retention implements ADR-0127: archive old audit events to gzip
// JSONL before deleting them, under a guarded exception to the append-only
// trigger. The package is deliberately separate from the scheduler so the
// archive path is testable without a daemon.
package retention

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// Options controls one archive pass.
type Options struct {
	// ArchiveDir is the destination directory. It is created with 0700.
	ArchiveDir string
	// Batch bounds the events selected per transaction. Zero means 10000.
	Batch int
	// DryRun reports what would move without writing or deleting.
	DryRun bool
}

// Result summarizes one archive pass.
type Result struct {
	Archived  int
	Files     int
	Bytes     int64
	FirstID   int64
	LastID    int64
	DryRun    bool
	Remaining int
}

// ErrNoArchiveDir reports a missing destination.
var ErrNoArchiveDir = errors.New("retention: archive dir is required")

// ArchiveEvents moves events older than cutoff into gzip JSONL archives and
// deletes them from the live table. It is idempotent: an existing archive file
// is verified rather than rewritten, and a second pass over the same range is
// a no-op.
func ArchiveEvents(ctx context.Context, st *store.Store, cutoff time.Time, opts Options) (Result, error) {
	if st == nil {
		return Result{}, errors.New("retention: nil store")
	}
	if opts.ArchiveDir == "" {
		return Result{}, ErrNoArchiveDir
	}
	if opts.Batch <= 0 {
		opts.Batch = 10000
	}
	if err := os.MkdirAll(opts.ArchiveDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("retention: creating archive dir: %w", err)
	}
	res := Result{DryRun: opts.DryRun}
	cutoffTS := cutoff.UTC().Format("2006-01-02T15:04:05.000Z")
	if opts.DryRun {
		n, err := CountEligible(ctx, st, cutoff)
		if err != nil {
			return res, err
		}
		res.Archived = n
		res.Remaining = n
		return res, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		rows, err := selectBatch(ctx, st, cutoffTS, opts.Batch)
		if err != nil {
			return res, err
		}
		if len(rows) == 0 {
			break
		}
		first, last := rows[0].ID, rows[len(rows)-1].ID
		path, digest, size, err := writeArchive(opts.ArchiveDir, first, last, rows)
		if err != nil {
			return res, err
		}
		if err := commitBatch(ctx, st, cutoffTS, first, last, len(rows), path, digest, size); err != nil {
			return res, err
		}
		res.Archived += len(rows)
		res.Files++
		res.Bytes += size
		res.FirstID = firstOr(res.FirstID, first)
		res.LastID = last
	}
	return res, nil
}

// CountEligible reports how many events are older than cutoff without changing
// anything (the status/dry-run surface).
func CountEligible(ctx context.Context, st *store.Store, cutoff time.Time) (int, error) {
	var n int
	err := st.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE ts < ?`,
		cutoff.UTC().Format("2006-01-02T15:04:05.000Z")).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("retention: counting eligible events: %w", err)
	}
	return n, nil
}

type eventRow struct {
	ID        int64           `json:"id"`
	TS        string          `json:"ts"`
	Category  string          `json:"category"`
	Level     string          `json:"level"`
	ProjectID string          `json:"project_id,omitempty"`
	JobID     string          `json:"job_id,omitempty"`
	Data      json.RawMessage `json:"data"`
}

func selectBatch(ctx context.Context, st *store.Store, cutoffTS string, batch int) ([]eventRow, error) {
	rows, err := st.ReadDB().QueryContext(ctx,
		`SELECT id, ts, category, level, COALESCE(project_id,''), COALESCE(job_id,''), data_json
		   FROM events WHERE ts < ? ORDER BY id ASC LIMIT ?`, cutoffTS, batch)
	if err != nil {
		return nil, fmt.Errorf("retention: selecting events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []eventRow
	for rows.Next() {
		var r eventRow
		var data string
		if err := rows.Scan(&r.ID, &r.TS, &r.Category, &r.Level, &r.ProjectID, &r.JobID, &data); err != nil {
			return nil, fmt.Errorf("retention: scanning event: %w", err)
		}
		r.Data = json.RawMessage(data)
		out = append(out, r)
	}
	return out, rows.Err()
}

// writeArchive writes (or verifies) one gzip JSONL file and returns its base
// name, SHA-256, and size. The file is fsynced before it is renamed into place,
// so a committed manifest never points at a partial archive.
func writeArchive(dir string, first, last int64, rows []eventRow) (string, string, int64, error) {
	name := fmt.Sprintf("events-%d-%d.jsonl.gz", first, last)
	final := filepath.Join(dir, name)
	if _, err := os.Stat(final); err == nil {
		digest, size, verr := fileDigest(final)
		return name, digest, size, verr
	}
	tmp := fmt.Sprintf("%s.tmp-%d", final, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", 0, fmt.Errorf("retention: creating archive: %w", err)
	}
	hash := sha256.New()
	mw := io.MultiWriter(f, hash)
	gz := gzip.NewWriter(mw)
	enc := json.NewEncoder(gz)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			_ = gz.Close()
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", "", 0, fmt.Errorf("retention: encoding archive: %w", err)
		}
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", "", 0, fmt.Errorf("retention: closing gzip: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", "", 0, fmt.Errorf("retention: fsync archive: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", "", 0, fmt.Errorf("retention: closing archive: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", "", 0, fmt.Errorf("retention: publishing archive: %w", err)
	}
	info, err := os.Stat(final)
	if err != nil {
		return "", "", 0, err
	}
	return name, hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func fileDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// commitBatch records the manifest, flips the guard, deletes the archived
// range, and restores the guard in one transaction. A crash rolls the whole
// thing back; the orphan archive file is reused on retry.
func commitBatch(ctx context.Context, st *store.Store, cutoffTS string, first, last int64, count int, path, digest string, size int64) error {
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("retention: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO retention_archives
		 (id, first_event_id, last_event_id, event_count, cutoff_ts, path, sha256, bytes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("%d-%d", first, last), first, last, count, cutoffTS, path, digest, size); err != nil {
		return fmt.Errorf("retention: manifest: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retention_guard SET active = 1 WHERE id = 1`); err != nil {
		return fmt.Errorf("retention: opening guard: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM events WHERE id >= ? AND id <= ? AND ts < ?`, first, last, cutoffTS)
	if err != nil {
		return fmt.Errorf("retention: deleting archived events: %w", err)
	}
	if n, _ := res.RowsAffected(); n != int64(count) {
		return fmt.Errorf("retention: deleted %d events, archived %d", n, count)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retention_guard SET active = 0 WHERE id = 1`); err != nil {
		return fmt.Errorf("retention: closing guard: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("retention: commit: %w", err)
	}
	return nil
}

// ListArchives returns the recorded archives, oldest first.
func ListArchives(ctx context.Context, st *store.Store) ([]Archive, error) {
	rows, err := st.ReadDB().QueryContext(ctx,
		`SELECT id, first_event_id, last_event_id, event_count, cutoff_ts, path, sha256, bytes, created_at
		   FROM retention_archives ORDER BY first_event_id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Archive
	for rows.Next() {
		var a Archive
		if err := rows.Scan(&a.ID, &a.FirstEventID, &a.LastEventID, &a.EventCount, &a.CutoffTS, &a.Path, &a.SHA256, &a.Bytes, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Archive is one manifest row.
type Archive struct {
	ID           string `json:"id"`
	FirstEventID int64  `json:"first_event_id"`
	LastEventID  int64  `json:"last_event_id"`
	EventCount   int    `json:"event_count"`
	CutoffTS     string `json:"cutoff_ts"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	CreatedAt    string `json:"created_at"`
}

func firstOr(v, fallback int64) int64 {
	if v != 0 {
		return v
	}
	return fallback
}
