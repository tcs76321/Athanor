package artifact

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// ErrNotFound reports an artifact ID that does not exist.
var ErrNotFound = errors.New("artifact not found")

// ContentMismatchError reports that an artifact's file no longer matches
// its recorded SHA-256 hash — fail loudly rather than serve bitrot.
type ContentMismatchError struct {
	ID       string
	WantHash string
	GotHash  string
}

func (e *ContentMismatchError) Error() string {
	return fmt.Sprintf("artifact %s content hash mismatch: recorded %s, actual %s", e.ID, e.WantHash, e.GotHash)
}

// Store persists artifacts: rows in SQLite, content as files under Dir.
type Store struct {
	db  *store.Store
	dir string
}

// NewStore returns an artifact store writing content files under dir.
func NewStore(s *store.Store, dir string) *Store {
	return &Store{db: s, dir: dir}
}

// CreateDraft writes a new version-1 draft artifact: content to disk
// (0600), row in SQLite with its SHA-256 hash, and an audit event.
func (a *Store) CreateDraft(ctx context.Context, projectID string, kind Kind, content []byte) (Artifact, error) {
	return a.CreateDraftFor(ctx, projectID, "", "", kind, content)
}

// CreateDraftFor is CreateDraft with optional task and job attribution,
// used by the engine (§9.2: artifacts belong to a job's execution).
func (a *Store) CreateDraftFor(ctx context.Context, projectID, taskID, jobID string, kind Kind, content []byte) (Artifact, error) {
	if !kind.Valid() {
		return Artifact{}, fmt.Errorf("invalid artifact kind %q (§9.1)", kind)
	}
	id := ids.New()
	// The row and its audit event commit together (ADR-0006): a crash must
	// not leave a committed draft with no `created` audit row.
	tx, err := a.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, fmt.Errorf("beginning artifact create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	art, err := a.persistTx(ctx, tx, artifactRow{
		ID: id, ProjectID: projectID, TaskID: taskID, JobID: jobID, Kind: kind,
		Version: 1, Status: StatusDraft,
	}, content)
	if err != nil {
		return Artifact{}, err
	}
	if err := a.audit(ctx, tx, art, map[string]any{
		"event": "created", "kind": string(kind), "hash": art.ContentHash,
	}); err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Artifact{}, fmt.Errorf("committing artifact create: %w", err)
	}
	return art, nil
}

// LatestForJob returns the newest artifact of a kind produced by a job.
func (a *Store) LatestForJob(ctx context.Context, jobID string, kind Kind) (Artifact, error) {
	row := a.db.DB().QueryRowContext(ctx,
		artifactSelect+` WHERE job_id = ? AND kind = ? ORDER BY version DESC LIMIT 1`,
		jobID, string(kind))
	art, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, fmt.Errorf("%w: no %s artifact for job %s", ErrNotFound, kind, jobID)
	}
	return art, err
}

// NewVersion creates the next version of an artifact, superseding the old
// one atomically: the new row, the old row's status flip, and the audit
// event commit together. The supersede chain is linear — an artifact that
// is already superseded cannot version again.
func (a *Store) NewVersion(ctx context.Context, supersedesID string, content []byte) (Artifact, error) {
	old, err := a.Get(ctx, supersedesID)
	if err != nil {
		return Artifact{}, err
	}
	if old.Status == StatusSuperseded {
		return Artifact{}, fmt.Errorf("artifact %s is already superseded; version from the current head instead", supersedesID)
	}

	id := ids.New()
	tx, err := a.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, fmt.Errorf("beginning artifact version: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	art, err := a.persistTx(ctx, tx, artifactRow{
		ID: id, ProjectID: old.ProjectID, TaskID: old.TaskID, JobID: old.JobID,
		SupersedesID: old.ID, Kind: old.Kind,
		Version: old.Version + 1, Status: StatusDraft,
	}, content)
	if err != nil {
		return Artifact{}, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE artifacts SET status = 'superseded' WHERE id = ? AND status = ?`,
		old.ID, string(old.Status))
	if err != nil {
		return Artifact{}, fmt.Errorf("superseding artifact: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Artifact{}, fmt.Errorf("artifact %s changed status concurrently; retry", old.ID)
	}
	if err := a.audit(ctx, tx, art, map[string]any{
		"event": "versioned", "supersedes": old.ID, "version": art.Version, "hash": art.ContentHash,
	}); err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Artifact{}, fmt.Errorf("committing artifact version: %w", err)
	}
	return art, nil
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
