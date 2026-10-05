package artifact

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// artifactRow is the insertable shape shared by CreateDraft/NewVersion.
type artifactRow struct {
	ID           string
	ProjectID    string
	TaskID       string
	JobID        string
	SupersedesID string
	Kind         Kind
	Version      int
	Status       Status
}

// persist writes content + row in its own transaction (CreateDraft).
func (a *Store) persist(ctx context.Context, row artifactRow, content []byte) (Artifact, error) {
	tx, err := a.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, fmt.Errorf("beginning artifact create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	art, err := a.persistTx(ctx, tx, row, content)
	if err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Artifact{}, fmt.Errorf("committing artifact create: %w", err)
	}
	return art, nil
}

// persistTx writes the content file and the row inside tx.
func (a *Store) persistTx(ctx context.Context, tx *sql.Tx, row artifactRow, content []byte) (Artifact, error) {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("creating artifact dir: %w", err)
	}
	path := filepath.Join(a.dir, row.ID)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return Artifact{}, fmt.Errorf("writing artifact content: %w", err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	_, err := tx.ExecContext(ctx, `
		INSERT INTO artifacts (id, project_id, task_id, job_id, supersedes_id, kind, version, status, storage_path, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.ProjectID, nullIfEmpty(row.TaskID), nullIfEmpty(row.JobID),
		nullIfEmpty(row.SupersedesID), string(row.Kind), row.Version,
		string(row.Status), path, hash,
	)
	if err != nil {
		_ = os.Remove(path) // don't leak orphaned content files
		return Artifact{}, fmt.Errorf("inserting artifact row: %w", err)
	}
	return Artifact{
		ID: row.ID, ProjectID: row.ProjectID, TaskID: row.TaskID, JobID: row.JobID,
		SupersedesID: row.SupersedesID, Kind: row.Kind, Version: row.Version,
		Status: row.Status, StoragePath: path, ContentHash: hash,
	}, nil
}

// audit appends an event under the jobs category (§28.1 has no artifact
// category; artifact lifecycle is job lifecycle).
func (a *Store) audit(ctx context.Context, tx *sql.Tx, art Artifact, data map[string]any) error {
	exec := a.db.DB().ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	raw, err := jsonMarshal(data)
	if err != nil {
		return fmt.Errorf("marshalling artifact event: %w", err)
	}
	_, err = exec(ctx,
		`INSERT INTO events (category, level, project_id, job_id, data_json)
		 VALUES ('jobs', 'info', ?, ?, ?)`,
		nullIfEmpty(art.ProjectID), nullIfEmpty(art.JobID), raw)
	if err != nil {
		return fmt.Errorf("appending artifact event: %w", err)
	}
	return nil
}

const artifactSelect = `
SELECT id, project_id, COALESCE(task_id, ''), COALESCE(job_id, ''), COALESCE(supersedes_id, ''),
       kind, version, status, COALESCE(storage_path, ''), COALESCE(content_hash, ''),
       COALESCE(git_commit, ''), created_at, updated_at
FROM artifacts`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanArtifact(sc scanner) (Artifact, error) {
	var art Artifact
	var kind, status string
	var createdAt, updatedAt string
	if err := sc.Scan(&art.ID, &art.ProjectID, &art.TaskID, &art.JobID, &art.SupersedesID,
		&kind, &art.Version, &status, &art.StoragePath, &art.ContentHash, &art.GitCommit,
		&createdAt, &updatedAt); err != nil {
		return Artifact{}, fmt.Errorf("scanning artifact: %w", err)
	}
	art.Kind, art.Status = Kind(kind), Status(status)
	var err error
	if art.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return Artifact{}, fmt.Errorf("parsing artifact timestamp %q: %w", createdAt, err)
	}
	if art.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return Artifact{}, fmt.Errorf("parsing artifact timestamp %q: %w", updatedAt, err)
	}
	return art, nil
}
