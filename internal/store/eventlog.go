package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Event levels (events.level CHECK, migration 0001).
type EventLevel string

const (
	EventDebug EventLevel = "debug"
	EventInfo  EventLevel = "info"
	EventWarn  EventLevel = "warn"
	EventError EventLevel = "error"
)

// Event is one append-only audit entry (ARCHITECTURE §28). Category uses
// the closed §28.1 set; membership is enforced where the set is owned
// (config validation, logging.Manager) — the store accepts any non-empty
// category so it never needs to depend on configuration.
//
// The project_id/job_id columns are deliberately NOT foreign keys
// (known-issues L14): the audit log must outlive the entities it references —
// a pruned project's or deleted job's history is still evidence — so unknown
// ids are accepted. See TestAppendEventReferencesAreLoose.
type Event struct {
	Category  string
	Level     EventLevel // "" defaults to EventInfo
	ProjectID string     // optional
	JobID     string     // optional
	Data      map[string]any
}

// EventRecord is a persisted event as returned by QueryEvents.
type EventRecord struct {
	ID        int64
	TS        time.Time
	Category  string
	Level     string
	ProjectID string
	JobID     string
	DataJSON  string
}

// AppendEvent inserts exactly one row into the append-only events table.
// There is deliberately no update or delete path anywhere on this type;
// the database triggers added in migration 0001 reject such statements
// regardless.
func (s *Store) AppendEvent(ctx context.Context, e Event) (int64, error) {
	if e.Category == "" {
		return 0, fmt.Errorf("event category must not be empty")
	}
	level := e.Level
	if level == "" {
		level = EventInfo
	}
	switch level {
	case EventDebug, EventInfo, EventWarn, EventError:
	default:
		return 0, fmt.Errorf("invalid event level %q (want debug|info|warn|error)", level)
	}

	data := []byte("{}")
	if len(e.Data) > 0 {
		raw, err := json.Marshal(e.Data)
		if err != nil {
			return 0, fmt.Errorf("marshalling event data: %w", err)
		}
		data = raw
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO events (category, level, project_id, job_id, data_json)
		 VALUES (?, ?, ?, ?, ?)`,
		e.Category, string(level), nullIfEmpty(e.ProjectID), nullIfEmpty(e.JobID), string(data),
	)
	if err != nil {
		return 0, fmt.Errorf("appending event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading event id: %w", err)
	}
	return id, nil
}

// EventFilter selects events for QueryEvents. Empty fields match anything;
// AfterID, when positive, selects only events with id greater than it — the
// streaming delta cursor (known-issues M9); Limit <= 0 means no limit.
type EventFilter struct {
	Category  string
	ProjectID string
	JobID     string
	AfterID   int64
	// BeforeID bounds a newest-first page: only rows with id < BeforeID.
	BeforeID int64
	// Level filters on the closed event level set (debug|info|warn|error).
	Level string
	// Query is a case-insensitive substring match over data_json. It is for
	// the read-only inspector, not a hot path.
	Query string
	// Desc returns newest first. The default is ascending (the SSE delta and
	// trajectory readers rely on insertion order).
	Desc  bool
	Limit int
}

// QueryEvents returns matching events ordered by insertion id ascending.
func (s *Store) QueryEvents(ctx context.Context, f EventFilter) ([]EventRecord, error) {
	q := `SELECT id, ts, category, level, COALESCE(project_id, ''), COALESCE(job_id, ''), data_json
	      FROM events WHERE 1=1`
	args := []any{}
	if f.Category != "" {
		q += ` AND category = ?`
		args = append(args, f.Category)
	}
	if f.ProjectID != "" {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	if f.JobID != "" {
		q += ` AND job_id = ?`
		args = append(args, f.JobID)
	}
	if f.AfterID > 0 {
		q += ` AND id > ?`
		args = append(args, f.AfterID)
	}
	if f.BeforeID > 0 {
		q += ` AND id < ?`
		args = append(args, f.BeforeID)
	}
	if f.Level != "" {
		q += ` AND level = ?`
		args = append(args, f.Level)
	}
	if f.Query != "" {
		q += ` AND data_json LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Query)+"%")
	}
	if f.Desc {
		q += ` ORDER BY id DESC`
	} else {
		q += ` ORDER BY id ASC`
	}
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EventRecord
	for rows.Next() {
		var r EventRecord
		var ts string
		if err := rows.Scan(&r.ID, &ts, &r.Category, &r.Level, &r.ProjectID, &r.JobID, &r.DataJSON); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return nil, fmt.Errorf("parsing event timestamp %q: %w", ts, err)
		}
		r.TS = parsed
		out = append(out, r)
	}
	return out, rows.Err()
}

// escapeLike escapes SQLite LIKE metacharacters so a user query is a literal
// substring. The caller pairs it with ESCAPE '\'.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// LatestEventID returns the largest event id matching f, or 0 when none
// match. A streaming client seeds its delta cursor with this instead of
// loading and scanning the whole table (known-issues M9). MAX(id) on the
// primary key is an index lookup, not a scan.
func (s *Store) LatestEventID(ctx context.Context, f EventFilter) (int64, error) {
	q := `SELECT COALESCE(MAX(id), 0) FROM events WHERE 1=1`
	args := []any{}
	if f.Category != "" {
		q += ` AND category = ?`
		args = append(args, f.Category)
	}
	if f.ProjectID != "" {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	if f.JobID != "" {
		q += ` AND job_id = ?`
		args = append(args, f.JobID)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		return 0, fmt.Errorf("reading latest event id: %w", err)
	}
	return id, nil
}

// nullIfEmpty maps Go empty strings to SQL NULL for optional columns.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
