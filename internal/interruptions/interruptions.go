// Package interruptions implements the §20.4 Interruption Queue (ROADMAP
// M6-T8c): a user adds a note while a job runs; the engine injects it at the
// next safe point and marks it injected. Notes never interrupt a generation
// mid-token.
package interruptions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// ErrNotFound reports a note ID that does not exist.
var ErrNotFound = errors.New("interruptions: note not found")

// Statuses.
const (
	StatusPending  = "pending"
	StatusInjected = "injected"
)

// Note is one queued interruption.
type Note struct {
	ID         string
	JobID      string
	Text       string
	Status     string
	CreatedAt  time.Time
	InjectedAt *time.Time
}

// Repo persists interruption notes.
type Repo struct{ store *store.Store }

// NewRepo returns a repo backed by s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

const noteColumns = `id, job_id, note, status, created_at, injected_at`

func scanNote(row interface{ Scan(...any) error }) (Note, error) {
	var n Note
	var createdAt string
	var injectedAt sql.NullString
	if err := row.Scan(&n.ID, &n.JobID, &n.Text, &n.Status, &createdAt, &injectedAt); err != nil {
		return Note{}, err
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Note{}, fmt.Errorf("parsing interruption timestamp %q: %w", createdAt, err)
	}
	n.CreatedAt = t
	if injectedAt.Valid {
		t, err := time.Parse(time.RFC3339, injectedAt.String)
		if err != nil {
			return Note{}, fmt.Errorf("parsing interruption injected_at %q: %w", injectedAt.String, err)
		}
		n.InjectedAt = &t
	}
	return n, nil
}

// Add queues a note for a job and audits it.
func (r *Repo) Add(ctx context.Context, jobID, note string) (Note, error) {
	if note == "" {
		return Note{}, fmt.Errorf("interruptions: note text must not be empty")
	}
	id := ids.New()
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO interruption_notes (id, job_id, note) VALUES (?, ?, ?)`,
		id, jobID, note,
	); err != nil {
		return Note{}, fmt.Errorf("inserting interruption note: %w", err)
	}
	_, _ = r.store.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: jobID,
		Data: map[string]any{"event": "interruption_queued", "note_id": id},
	})
	return r.Get(ctx, id)
}

// Get loads one note.
func (r *Repo) Get(ctx context.Context, id string) (Note, error) {
	row := r.store.DB().QueryRowContext(ctx, `SELECT `+noteColumns+` FROM interruption_notes WHERE id = ?`, id)
	n, err := scanNote(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Note{}, fmt.Errorf("loading interruption note: %w", err)
	}
	return n, nil
}

// Pending returns a job's not-yet-injected notes, oldest first.
func (r *Repo) Pending(ctx context.Context, jobID string) ([]Note, error) {
	return r.query(ctx, `WHERE job_id = ? AND status = 'pending' ORDER BY created_at ASC, id ASC`, jobID)
}

// List returns every note for a job, oldest first.
func (r *Repo) List(ctx context.Context, jobID string) ([]Note, error) {
	return r.query(ctx, `WHERE job_id = ? ORDER BY created_at ASC, id ASC`, jobID)
}

// MarkInjected marks notes injected (idempotent) and audits each.
func (r *Repo) MarkInjected(ctx context.Context, ids []string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range ids {
		if _, err := r.store.DB().ExecContext(ctx,
			`UPDATE interruption_notes SET status = 'injected', injected_at = COALESCE(injected_at, ?) WHERE id = ? AND status = 'pending'`,
			now, id,
		); err != nil {
			return fmt.Errorf("marking interruption note injected: %w", err)
		}
	}
	return nil
}

func (r *Repo) query(ctx context.Context, tail string, args ...any) ([]Note, error) {
	rows, err := r.store.DB().QueryContext(ctx, `SELECT `+noteColumns+` FROM interruption_notes `+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("listing interruption notes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning interruption note: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating interruption notes: %w", err)
	}
	return out, nil
}
