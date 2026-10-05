package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const jobSelect = `
SELECT id, task_id, project_id, state, COALESCE(paused_from, ''),
       COALESCE(awaiting_from, ''), COALESCE(recovery_flag, ''), attempt,
       started_at, finished_at, created_at, updated_at
FROM jobs`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanJob(sc scanner) (Job, error) {
	var j Job
	var state, pausedFrom, awaitingFrom, recovery string
	var startedAt, finishedAt sql.NullString
	var createdAt, updatedAt string
	if err := sc.Scan(&j.ID, &j.TaskID, &j.ProjectID, &state, &pausedFrom,
		&awaitingFrom, &recovery, &j.Attempt, &startedAt, &finishedAt, &createdAt, &updatedAt); err != nil {
		return Job{}, fmt.Errorf("scanning job: %w", err)
	}
	j.State = State(state)
	j.PausedFrom = State(pausedFrom)
	j.AwaitingFrom = State(awaitingFrom)
	j.RecoveryFlag = recovery
	var err error
	if j.CreatedAt, err = parseTS(createdAt); err != nil {
		return Job{}, err
	}
	if j.UpdatedAt, err = parseTS(updatedAt); err != nil {
		return Job{}, err
	}
	if startedAt.Valid {
		t, err := parseTS(startedAt.String)
		if err != nil {
			return Job{}, err
		}
		j.StartedAt = &t
	}
	if finishedAt.Valid {
		t, err := parseTS(finishedAt.String)
		if err != nil {
			return Job{}, err
		}
		j.FinishedAt = &t
	}
	return j, nil
}

func parseTS(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing job timestamp %q: %w", s, err)
	}
	return t, nil
}

// appendEvent writes one audit event. When tx is non-nil the event joins
// the caller's transaction so the audit trail commits atomically with the
// state change it describes (ADR-0006); the statement mirrors
// store.AppendEvent's INSERT, the only sanctioned shape for the
// append-only events table.
func (r *Repository) appendEvent(ctx context.Context, tx *sql.Tx, jobID, projectID string, data map[string]any) error {
	exec := r.store.DB().ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	_, err := exec(ctx,
		`INSERT INTO events (category, level, project_id, job_id, data_json)
		 VALUES (?, 'info', ?, ?, ?)`,
		"jobs", nullIfEmpty(projectID), nullIfEmpty(jobID), mustJSON(data),
	)
	if err != nil {
		return fmt.Errorf("appending job event: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		// Event data in this package is always maps of strings; a marshal
		// failure is a programming error, not a runtime condition.
		return "{}"
	}
	return string(raw)
}
