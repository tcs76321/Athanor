package artifact

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// Get loads one artifact by ID.
func (a *Store) Get(ctx context.Context, id string) (Artifact, error) {
	row := a.db.DB().QueryRowContext(ctx, artifactSelect+` WHERE id = ?`, id)
	art, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return art, err
}

// ListByProject returns a project's artifacts, newest first.
func (a *Store) ListByProject(ctx context.Context, projectID string) ([]Artifact, error) {
	rows, err := a.db.DB().QueryContext(ctx,
		artifactSelect+` WHERE project_id = ? ORDER BY created_at DESC, id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Artifact
	for rows.Next() {
		art, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, art)
	}
	return out, rows.Err()
}

// ListByJob returns the artifacts a single job
// produced, in creation order (oldest first). M3-T1
// follow-up (ROADMAP §7): the E1 dialectical-loop
// test in `internal/engine/multicandidate_test.go`
// previously ran `env.db.DB().QueryContext(...)`
// directly to count proposal artifacts. Adding this
// method routes the test through the typed store
// and removes the raw SQL. The shape mirrors
// `evaluation.Repo.ListByJob` (`internal/evaluation/repo.go`).
func (a *Store) ListByJob(ctx context.Context, jobID string) ([]Artifact, error) {
	rows, err := a.db.DB().QueryContext(ctx,
		artifactSelect+` WHERE job_id = ? ORDER BY created_at ASC, id ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts by job: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Artifact
	for rows.Next() {
		art, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, art)
	}
	return out, rows.Err()
}

// ListAccepted returns up to limit accepted artifacts, newest first. It is the
// M5-T6 daydream semantic-compaction source's enumeration: an accepted
// documentation artifact is archival memory (§10.2). limit <= 0 means no limit.
func (a *Store) ListAccepted(ctx context.Context, limit int) ([]Artifact, error) {
	q := artifactSelect + ` WHERE status = ? ORDER BY created_at DESC, id ASC`
	args := []any{string(StatusAccepted)}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := a.db.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing accepted artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Artifact
	for rows.Next() {
		art, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, art)
	}
	return out, rows.Err()
}

// LatestAcceptedByProject returns the project's most recently accepted
// artifact, or ErrNotFound when the project has no accepted artifact
// yet. Used by §13.1 Phase 6 (Comparing) to find the "previous" side
// of the §19.3 comparison rule; the comparison phase picks the
// project's current best, not just any non-rejected artifact.
//
// M3-T1 owns this. Git-as-undo (F3-T5, ADR-0030) does not use it — the
// commit goes into a managed namespace, not on top of the previous head —
// but the phaseCompare path still uses it to find the previous side.
// There can be at most one accepted
// artifact per project at a time (§9.3: accepting a new one
// supersedes the old), but the query orders by version DESC then
// created_at DESC to stay correct if that invariant ever relaxes.
func (a *Store) LatestAcceptedByProject(ctx context.Context, projectID string) (Artifact, error) {
	row := a.db.DB().QueryRowContext(ctx,
		artifactSelect+` WHERE project_id = ? AND status = ? ORDER BY version DESC, created_at DESC, id ASC LIMIT 1`,
		projectID, string(StatusAccepted))
	art, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, fmt.Errorf("%w: no accepted artifact for project %s", ErrNotFound, projectID)
	}
	return art, err
}

// SetStatus applies a §9.3 status transition.
func (a *Store) SetStatus(ctx context.Context, id string, to Status) error {
	art, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidateTransition(art.Status, to); err != nil {
		return err
	}
	res, err := a.db.DB().ExecContext(ctx,
		`UPDATE artifacts SET status = ? WHERE id = ? AND status = ?`,
		string(to), id, string(art.Status))
	if err != nil {
		return fmt.Errorf("updating artifact status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("artifact %s changed status concurrently; retry", id)
	}
	return a.audit(ctx, nil, art, map[string]any{
		"event": "status", "from": string(art.Status), "to": string(to),
	})
}

// SetGitCommit records the SHA of the commit an accepted artifact was
// recorded to on an agent branch (§9.2, §14; ADR-0030). It is a recording
// operation, not a §9.3 status change: acceptance already happened, and
// this attaches the Git identity that makes it undoable. The audit event
// is appended separately from the update; the engine's caller treats a
// missing commit as "skip + audit", never as a failed acceptance.
func (a *Store) SetGitCommit(ctx context.Context, id, commit string) error {
	art, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := a.db.DB().ExecContext(ctx,
		`UPDATE artifacts SET git_commit = ? WHERE id = ?`, nullIfEmpty(commit), id); err != nil {
		return fmt.Errorf("recording artifact git commit: %w", err)
	}
	return a.audit(ctx, nil, art, map[string]any{
		"event": "git_commit", "commit": commit,
	})
}

// SupersedeAndAccept is M3-T3 commit 3.2: the §9.3 status
// transition that promotes a candidate to accepted and
// demotes the previous accepted to superseded, atomically.
//
// The two non-atomic SetStatus calls that phaseCompare used
// before this commit (supersede the previous, then accept
// the new) had a window where a crash between the two left
// the project with *zero* accepted artifacts. The single-
// transaction form here closes that window: either both
// updates commit, or neither does.
//
// Both updates are CAS-guarded on the current status:
//   - previousID must currently be `accepted` (else the
//     supersede has no effect, and the function errors
//     out so the caller can recover).
//   - newID must currently be `candidate` (else the
//     accept is a no-op, and the function errors out).
//
// The error returns are sentinel-friendly: callers (the
// engine's phaseCompare) match on `IllegalStatusError`
// when the transition shape is wrong, and on a generic
// status-concurrency error when the CAS fails.
func (a *Store) SupersedeAndAccept(ctx context.Context, previousID, newID string) error {
	if previousID == "" {
		return fmt.Errorf("SupersedeAndAccept: previousID is empty")
	}
	if newID == "" {
		return fmt.Errorf("SupersedeAndAccept: newID is empty")
	}
	// Read both artifacts first so the audit events carry
	// the full before/after status. Neither artifact's
	// transition is pre-flight-validated: the SQL CAS
	// (WHERE status = ...) is the actual gate for both.
	// Pre-flight validating would reject states like
	// `superseded → superseded` or `rejected → accepted`
	// with "illegal transition" errors, but those are
	// stale-input cases that the CAS catches naturally.
	prev, err := a.Get(ctx, previousID)
	if err != nil {
		return err
	}
	new, err := a.Get(ctx, newID)
	if err != nil {
		return err
	}

	tx, err := a.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("SupersedeAndAccept: beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// CAS: previous must be currently `accepted`.
	res, err := tx.ExecContext(ctx,
		`UPDATE artifacts SET status = ? WHERE id = ? AND status = ?`,
		string(StatusSuperseded), previousID, string(StatusAccepted))
	if err != nil {
		return fmt.Errorf("SupersedeAndAccept: supersede update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("SupersedeAndAccept: previous %s is no longer `accepted` (concurrent transition)", previousID)
	}
	// CAS: new must be currently `candidate`.
	res, err = tx.ExecContext(ctx,
		`UPDATE artifacts SET status = ? WHERE id = ? AND status = ?`,
		string(StatusAccepted), newID, string(StatusCandidate))
	if err != nil {
		return fmt.Errorf("SupersedeAndAccept: accept update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("SupersedeAndAccept: new %s is no longer `candidate` (concurrent transition)", newID)
	}
	// Audit events commit with the same transaction so a
	// crash after commit can't leave the artifacts updated
	// without their audit rows (the §28.1 invariant).
	if err := a.audit(ctx, tx, prev, map[string]any{
		"event": "status", "from": string(prev.Status), "to": string(StatusSuperseded),
	}); err != nil {
		return err
	}
	if err := a.audit(ctx, tx, new, map[string]any{
		"event": "status", "from": string(new.Status), "to": string(StatusAccepted),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("SupersedeAndAccept: commit: %w", err)
	}
	return nil
}

// ReadContent returns the artifact's file content, verifying the recorded
// SHA-256 hash first: silent bitrot is never acceptable (fail loudly).
func (a *Store) ReadContent(ctx context.Context, id string) ([]byte, error) {
	art, err := a.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if art.StoragePath == "" {
		return nil, fmt.Errorf("artifact %s has no stored content", id)
	}
	data, err := os.ReadFile(art.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("reading artifact %s content: %w", id, err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != art.ContentHash {
		return nil, &ContentMismatchError{ID: id, WantHash: art.ContentHash, GotHash: got}
	}
	return data, nil
}
