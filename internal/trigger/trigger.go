// Package trigger owns a project's scheduled and event-driven cycle starts
// (ADR-0134). It is deliberately small: the runner claims a due slot, calls a
// launcher, and records the next slot. It never executes work itself; the
// launcher compiles the template and hands it to the existing scheduler.
package trigger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/backup"
	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Kind is how a trigger starts a cycle.
type Kind string

const (
	KindManual   Kind = "manual"
	KindSchedule Kind = "schedule"
	KindEvent    Kind = "event"
)

// ErrNotFound is returned for an unknown trigger.
var ErrNotFound = errors.New("trigger: not found")

// timeFormat is the canonical stored form. RFC3339 in UTC sorts lexicographically
// and is what next_run_at comparisons rely on.
const timeFormat = time.RFC3339

// Record is one trigger row.
type Record struct {
	ID         string
	ProjectID  string
	TemplateID string
	Kind       Kind
	Spec       string
	// Timezone is the IANA location the cron fields are interpreted in.
	// next_run_at stays UTC.
	Timezone       string
	Enabled        bool
	NextRunAt      string
	LastRunAt      string
	IdempotencyKey string
	CreatedAt      string
	UpdatedAt      string
}

// Repo persists triggers.
type Repo struct{ store *store.Store }

// NewRepo returns a repository over s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

const columns = `id, project_id, template_id, kind, spec, COALESCE(timezone, 'UTC'), enabled,
	COALESCE(next_run_at, ''), COALESCE(last_run_at, ''), idempotency_key,
	created_at, updated_at`

// Ensure creates or updates the trigger for a (project, template, kind) in
// UTC. It is the compatibility wrapper for callers with no timezone choice.
func (r *Repo) Ensure(ctx context.Context, projectID, templateID string, kind Kind, spec string, enabled bool) (Record, error) {
	return r.EnsureIn(ctx, projectID, templateID, kind, spec, enabled, "UTC")
}

// EnsureIn creates or updates a trigger whose schedule is interpreted in
// timezone (an IANA name; empty means UTC). A schedule's next_run_at is the
// next matching minute in that location, stored as UTC.
func (r *Repo) EnsureIn(ctx context.Context, projectID, templateID string, kind Kind, spec string, enabled bool, timezone string) (Record, error) {
	if strings.TrimSpace(projectID) == "" {
		return Record{}, fmt.Errorf("trigger: project id is required")
	}
	if !validKind(kind) {
		return Record{}, fmt.Errorf("trigger: unknown kind %q", kind)
	}
	spec = strings.TrimSpace(spec)
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	loc, locErr := time.LoadLocation(timezone)
	if locErr != nil {
		return Record{}, fmt.Errorf("trigger: timezone %q: %w", timezone, locErr)
	}
	if kind == KindSchedule {
		if _, err := backup.ParseCron(spec); err != nil {
			return Record{}, fmt.Errorf("trigger: %w", err)
		}
	}
	now := time.Now().UTC()
	var existing string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT id FROM triggers WHERE project_id = ? AND template_id = ? AND kind = ? LIMIT 1`,
		projectID, templateID, string(kind)).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		next := ""
		if kind == KindSchedule {
			n, ok := NextRunIn(spec, now, loc)
			if !ok {
				return Record{}, fmt.Errorf("trigger: schedule %q never fires", spec)
			}
			next = n.Format(timeFormat)
		}
		id := ids.New()
		if _, err := r.store.DB().ExecContext(ctx,
			`INSERT INTO triggers (id, project_id, template_id, kind, spec, timezone, enabled, next_run_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))`,
			id, projectID, templateID, string(kind), spec, timezone, enabled, next,
		); err != nil {
			return Record{}, fmt.Errorf("trigger: insert: %w", err)
		}
		return r.Get(ctx, id)
	case err != nil:
		return Record{}, fmt.Errorf("trigger: lookup: %w", err)
	}

	next := ""
	if kind == KindSchedule {
		n, ok := NextRunIn(spec, now, loc)
		if !ok {
			return Record{}, fmt.Errorf("trigger: schedule %q never fires", spec)
		}
		next = n.Format(timeFormat)
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`UPDATE triggers SET kind = ?, spec = ?, timezone = ?, enabled = ?, next_run_at = NULLIF(?, ''),
		 updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		string(kind), spec, timezone, enabled, next, existing,
	); err != nil {
		return Record{}, fmt.Errorf("trigger: update: %w", err)
	}
	return r.Get(ctx, existing)
}

// Get loads one trigger.
func (r *Repo) Get(ctx context.Context, id string) (Record, error) {
	row := r.store.DB().QueryRowContext(ctx, `SELECT `+columns+` FROM triggers WHERE id = ?`, id)
	tr, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("trigger: load: %w", err)
	}
	return tr, nil
}

// Due returns enabled schedule triggers whose slot has arrived, oldest first.
func (r *Repo) Due(ctx context.Context, now time.Time, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT `+columns+` FROM triggers
		 WHERE enabled = 1 AND kind = ? AND next_run_at IS NOT NULL AND next_run_at <= ?
		 ORDER BY next_run_at ASC LIMIT ?`,
		string(KindSchedule), now.UTC().Format(timeFormat), limit)
	if err != nil {
		return nil, fmt.Errorf("trigger: due: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		tr, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("trigger: scan: %w", err)
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// ListByProject returns a project's triggers, oldest first.
func (r *Repo) ListByProject(ctx context.Context, projectID string) ([]Record, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT `+columns+` FROM triggers WHERE project_id = ? ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("trigger: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		tr, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// MarkRan claims a fired slot and records the next one. It is the at-most-once
// boundary: the slot is claimed before the launcher runs, so a crash cannot
// start the same slot twice. A launch failure is audited by the caller and the
// next slot is still advanced.
func (r *Repo) MarkRan(ctx context.Context, id, slot, ranAt string, next time.Time) error {
	if slot == "" {
		return fmt.Errorf("trigger: slot is required")
	}
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE triggers SET last_run_at = ?, next_run_at = ?, idempotency_key = ?,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		ranAt, next.UTC().Format(timeFormat), id+"@"+slot, id)
	if err != nil {
		return fmt.Errorf("trigger: mark ran: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEnabled enables or disables a trigger. Disabling clears the next slot so a
// stale slot does not fire when it is re-enabled.
func (r *Repo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	var next any
	if enabled {
		tr, err := r.Get(ctx, id)
		if err != nil {
			return err
		}
		if tr.Kind == KindSchedule {
			loc, lerr := time.LoadLocation(tr.Timezone)
			if lerr != nil {
				loc = time.UTC
			}
			n, ok := NextRunIn(tr.Spec, time.Now().UTC(), loc)
			if ok {
				next = n.Format(timeFormat)
			}
		}
	}
	_, err := r.store.DB().ExecContext(ctx,
		`UPDATE triggers SET enabled = ?, next_run_at = ?,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		enabled, next, id)
	if err != nil {
		return fmt.Errorf("trigger: set enabled: %w", err)
	}
	return nil
}

// NextRun is the next matching minute after from, in UTC.
func NextRun(spec string, from time.Time) (time.Time, bool) {
	return NextRunIn(spec, from, time.UTC)
}

// NextRunIn interprets the cron fields in loc and returns the next matching
// minute as UTC. A nil/invalid location falls back to UTC; DST is whatever
// time.Add produces in the location, which is the same rule a user expects
// from a local calendar schedule.
func NextRunIn(spec string, from time.Time, loc *time.Location) (time.Time, bool) {
	s, err := backup.ParseCron(spec)
	if err != nil {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	n, ok := s.Next(from.In(loc))
	if !ok {
		return time.Time{}, false
	}
	return n.UTC(), true
}

func scan(row interface{ Scan(...any) error }) (Record, error) {
	var tr Record
	var kind string
	err := row.Scan(&tr.ID, &tr.ProjectID, &tr.TemplateID, &kind, &tr.Spec, &tr.Timezone,
		&tr.Enabled, &tr.NextRunAt, &tr.LastRunAt, &tr.IdempotencyKey,
		&tr.CreatedAt, &tr.UpdatedAt)
	tr.Kind = Kind(kind)
	return tr, err
}

func validKind(k Kind) bool {
	switch k {
	case KindManual, KindSchedule, KindEvent:
		return true
	}
	return false
}

// SortByNextRun orders records by their next slot, empty slots last.
func SortByNextRun(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].NextRunAt == "" {
			return false
		}
		if records[j].NextRunAt == "" {
			return true
		}
		return records[i].NextRunAt < records[j].NextRunAt
	})
}
