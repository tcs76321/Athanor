// Package alarms implements the §22.3 alarm system (ROADMAP M7-T3): the
// category/level model, a persistent active/resolved store, and a Monitor
// that evaluates detectors against a snapshot of the daemon's state.
//
// Levels escalate: notice (logged), warning (UI), alert (pause the job), and
// critical (freeze the system). A critical alarm calls the injected Freezer,
// so the §22 kill switch is the single freeze authority.
//
// Detection is split from I/O:
//
//   - Detect is a pure function over a Snapshot (recent outcomes, active jobs,
//     repeated tool calls, event flags). Every category is a table test.
//   - the Loader builds a Snapshot from SQLite; the Monitor ties them together
//     and calls Service.Raise.
package alarms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Category is a §22.3 alarm category.
type Category string

// The §22.3 categories.
const (
	CategoryLoop             Category = "loop"
	CategoryResource         Category = "resource"
	CategorySecurity         Category = "security"
	CategoryQuality          Category = "quality"
	CategoryStuck            Category = "stuck"
	CategoryHallucination    Category = "hallucination"
	CategoryBudget           Category = "budget"
	CategorySelfModification Category = "self_modification"
	CategoryDrift            Category = "drift"
)

// Level is a §22.3 alarm level.
type Level string

// The §22.3 levels, in ascending severity.
const (
	LevelNotice   Level = "notice"
	LevelWarning  Level = "warning"
	LevelAlert    Level = "alert"
	LevelCritical Level = "critical"
)

// ValidCategory reports whether c is a §22.3 category.
func ValidCategory(c Category) bool {
	switch c {
	case CategoryLoop, CategoryResource, CategorySecurity, CategoryQuality,
		CategoryStuck, CategoryHallucination, CategoryBudget,
		CategorySelfModification, CategoryDrift:
		return true
	default:
		return false
	}
}

// ValidLevel reports whether l is a §22.3 level.
func ValidLevel(l Level) bool {
	switch l {
	case LevelNotice, LevelWarning, LevelAlert, LevelCritical:
		return true
	default:
		return false
	}
}

// DefaultLevel returns the §22.3 default level for a category.
func DefaultLevel(c Category) Level {
	switch c {
	case CategorySecurity, CategorySelfModification, CategoryDrift:
		return LevelCritical
	case CategoryQuality, CategoryHallucination:
		return LevelWarning
	default:
		return LevelAlert
	}
}

// Alarm is one persisted record.
type Alarm struct {
	ID         string    `json:"id"`
	Category   Category  `json:"category"`
	Level      Level     `json:"level"`
	Message    string    `json:"message"`
	JobID      string    `json:"job_id,omitempty"`
	ProjectID  string    `json:"project_id,omitempty"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

// IsActive reports whether the alarm is unresolved.
func (a Alarm) IsActive() bool { return a.Status == "active" }

// Freezer is the §22 kill-switch surface a critical alarm drives.
type Freezer interface {
	Frozen() bool
	Freeze(ctx context.Context) error
}

// Service persists alarms and escalates critical ones to the freezer.
type Service struct {
	store   *store.Store
	freezer Freezer
}

// NewService returns a service backed by s. A nil freezer disables the
// critical-freezes-system behavior (tests, minimal setups).
func NewService(s *store.Store, f Freezer) *Service {
	return &Service{store: s, freezer: f}
}

// ErrNotFound is returned when an alarm id does not exist.
var ErrNotFound = errors.New("alarms: not found")

// Raise validates and persists an new alarm. An identical active alarm (same
// category, message, and scope) is a no-op that returns the existing row, so a
// recurring detector condition does not spam the table. A critical alarm
// freezes the system via the Freezer and audits the freeze.
func (s *Service) Raise(ctx context.Context, in Alarm) (Alarm, error) {
	if !ValidCategory(in.Category) {
		return Alarm{}, fmt.Errorf("alarms: unknown category %q", in.Category)
	}
	if in.Level == "" {
		in.Level = DefaultLevel(in.Category)
	}
	if !ValidLevel(in.Level) {
		return Alarm{}, fmt.Errorf("alarms: unknown level %q", in.Level)
	}
	if in.Message == "" {
		return Alarm{}, fmt.Errorf("alarms: message required")
	}

	if existing, ok, err := s.findActive(ctx, in); err != nil {
		return Alarm{}, err
	} else if ok {
		return existing, nil
	}

	id := ids.New()
	if _, err := s.store.DB().ExecContext(ctx,
		`INSERT INTO alarms (id, category, level, message, job_id, project_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, string(in.Category), string(in.Level), in.Message,
		nullIfEmpty(in.JobID), nullIfEmpty(in.ProjectID),
	); err != nil {
		return Alarm{}, fmt.Errorf("alarms: inserting: %w", err)
	}
	in.ID = id
	in.Status = "active"

	_, _ = s.store.AppendEvent(ctx, store.Event{
		Category: "alarms", ProjectID: in.ProjectID, JobID: in.JobID,
		Data: map[string]any{
			"event": "alarm_raised", "alarm_id": id,
			"category": string(in.Category), "level": string(in.Level), "message": in.Message,
		},
	})

	if in.Level == LevelCritical && s.freezer != nil && !s.freezer.Frozen() {
		if err := s.freezer.Freeze(ctx); err != nil {
			return in, fmt.Errorf("alarms: freezing on critical: %w", err)
		}
	}
	return s.Get(ctx, id)
}

// Resolve marks an alarm resolved.
func (s *Service) Resolve(ctx context.Context, id string) error {
	res, err := s.store.DB().ExecContext(ctx,
		`UPDATE alarms SET status='resolved', resolved_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE id = ? AND status = 'active'`, id)
	if err != nil {
		return fmt.Errorf("alarms: resolving: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either unknown or already resolved; distinguish for the caller.
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Get loads one alarm.
func (s *Service) Get(ctx context.Context, id string) (Alarm, error) {
	row := s.store.DB().QueryRowContext(ctx, alarmColumns+` WHERE id = ?`, id)
	a, err := scanAlarm(row)
	if errors.Is(err, errNoRows) {
		return Alarm{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return a, err
}

// Active returns unresolved alarms, most severe first then newest.
func (s *Service) Active(ctx context.Context) ([]Alarm, error) {
	return s.query(ctx, `WHERE status='active' ORDER BY
		CASE level WHEN 'critical' THEN 0 WHEN 'alert' THEN 1 WHEN 'warning' THEN 2 ELSE 3 END,
		created_at DESC`)
}

// List returns alarms newest first (limit <= 0 means all).
func (s *Service) List(ctx context.Context, limit int) ([]Alarm, error) {
	q := alarmColumns
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	return s.queryRaw(ctx, q)
}

func (s *Service) findActive(ctx context.Context, in Alarm) (Alarm, bool, error) {
	row := s.store.DB().QueryRowContext(ctx,
		alarmColumns+` WHERE status='active' AND category = ? AND message = ?
		 AND COALESCE(job_id,'') = ? AND COALESCE(project_id,'') = ?`,
		string(in.Category), in.Message, in.JobID, in.ProjectID)
	a, err := scanAlarm(row)
	if errors.Is(err, errNoRows) {
		return Alarm{}, false, nil
	}
	if err != nil {
		return Alarm{}, false, err
	}
	return a, true, nil
}

func (s *Service) query(ctx context.Context, where string) ([]Alarm, error) {
	return s.queryRaw(ctx, alarmColumns+" "+where)
}

func (s *Service) queryRaw(ctx context.Context, q string) ([]Alarm, error) {
	rows, err := s.store.DB().QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("alarms: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Alarm
	for rows.Next() {
		a, err := scanAlarm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const alarmColumns = `SELECT id, category, level, message, COALESCE(job_id,''), COALESCE(project_id,''),
	status, created_at, COALESCE(resolved_at,'') FROM alarms`

var errNoRows = errors.New("alarms: no rows")

type scanner interface{ Scan(...any) error }

func scanAlarm(row scanner) (Alarm, error) {
	var a Alarm
	var category, level, status, created, resolved string
	if err := row.Scan(&a.ID, &category, &level, &a.Message, &a.JobID, &a.ProjectID, &status, &created, &resolved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Alarm{}, errNoRows
		}
		return Alarm{}, err
	}
	a.Category = Category(category)
	a.Level = Level(level)
	a.Status = status
	var err error
	if a.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return Alarm{}, fmt.Errorf("alarms: parsing created_at %q: %w", created, err)
	}
	if resolved != "" {
		if a.ResolvedAt, err = time.Parse(time.RFC3339, resolved); err != nil {
			return Alarm{}, fmt.Errorf("alarms: parsing resolved_at %q: %w", resolved, err)
		}
	}
	return a, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
