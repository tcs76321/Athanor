package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// ErrNotFound reports a job ID that does not exist.
var ErrNotFound = errors.New("job not found")

// ErrConcurrentTransition reports that a compare-and-swap transition lost
// to another writer — the job moved between read and update. The caller
// reloads and retries or surfaces the conflict; it never silently
// overwrites (ADR-0006).
var ErrConcurrentTransition = errors.New("job transitioned concurrently")

// Job is one persisted job row (§8.3, M1 subset).
type Job struct {
	ID           string
	TaskID       string
	ProjectID    string
	State        State
	PausedFrom   State // set only while State == paused
	AwaitingFrom State // set only while State == awaiting_approval
	RecoveryFlag string
	Attempt      int
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Repository persists jobs and their transitions on the store.
type Repository struct {
	store *store.Store
}

// NewRepository returns a repository backed by s.
func NewRepository(s *store.Store) *Repository {
	return &Repository{store: s}
}

// Create inserts a new queued job for a task and appends its creation
// event in the same transaction.
func (r *Repository) Create(ctx context.Context, taskID, projectID string) (Job, error) {
	id := ids.New()
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO jobs (id, task_id, project_id, state) VALUES (?, ?, ?, ?)`,
		id, taskID, projectID, string(StateQueued),
	); err != nil {
		return Job{}, fmt.Errorf("creating job: %w", err)
	}
	if err := r.appendEvent(ctx, nil, id, projectID, map[string]any{"event": "created"}); err != nil {
		return Job{}, err
	}
	return r.Get(ctx, id)
}

// Get loads one job by ID.
func (r *Repository) Get(ctx context.Context, id string) (Job, error) {
	row := r.store.DB().QueryRowContext(ctx, jobSelect+` WHERE id = ?`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return j, err
}

// Active returns every job in a non-terminal state — the set crash
// recovery must inspect at startup (§23.6).
func (r *Repository) Active(ctx context.Context) ([]Job, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		jobSelect+` WHERE state NOT IN ('completed','failed','cancelled') ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing active jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ByTask returns a task's jobs, oldest first. The scheduler reads it to
// decide whether a task already has an active or terminal job (M6-T2).
func (r *Repository) ByTask(ctx context.Context, taskID string) ([]Job, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		jobSelect+` WHERE task_id = ? ORDER BY rowid ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("listing jobs for task: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Terminal returns up to limit terminal jobs (completed/failed/cancelled),
// most recently updated first. It is the M5-T6 daydream memory-consolidation
// source's enumeration: a terminal job's event log is episodic memory (§10.2).
// limit <= 0 means no limit.
func (r *Repository) Terminal(ctx context.Context, limit int) ([]Job, error) {
	q := jobSelect + ` WHERE state IN ('completed','failed','cancelled') ORDER BY updated_at DESC, id ASC`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing terminal jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
