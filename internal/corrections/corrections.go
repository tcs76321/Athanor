// Package corrections implements the ARCHITECTURE §18 CorrectionRecord store
// (ROADMAP M6-T6): prescriptive negative feedback derived from a failure.
//
// Capture maps each §18.1 source to a default category/severity/derived rule
// and persists a record; the §18.4 mandatory rejection form is enforced for
// user-driven sources. Repo also lists and mutes/promotes records.
package corrections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Errors.
var (
	ErrNotFound           = errors.New("corrections: record not found")
	ErrUnknownSource      = errors.New("corrections: unknown feedback source")
	ErrIncompleteFeedback = errors.New("corrections: incomplete rejection feedback")
	ErrInvalidField       = errors.New("corrections: invalid field value")
)

// Source is a §18.1 feedback source.
type Source string

// The §18.1 sources.
const (
	SourceUserRejection    Source = "user_rejection"
	SourceUserCorrection   Source = "user_correction"
	SourceTestFailure      Source = "test_failure"
	SourceEvaluatorFailure Source = "evaluator_failure"
	SourceSecurityScan     Source = "security_scan"
	SourceRuntimeError     Source = "runtime_error"
	SourceLoopDetection    Source = "loop_detection"
	SourceBudgetExhaustion Source = "budget_exhaustion"
	SourceHallucinatedPath Source = "hallucinated_path"
)

// Categories (§18.2).
const (
	CategoryArchitecture  = "architecture"
	CategoryStyle         = "style"
	CategoryTesting       = "testing"
	CategorySecurity      = "security"
	CategoryPerformance   = "performance"
	CategoryTooling       = "tooling"
	CategoryDocumentation = "documentation"
	CategoryOther         = "other"
)

// Severities (§18.2; the schema CHECK is the closed set).
const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// Scopes (§18.2).
const (
	ScopeProject = "project"
	ScopeGlobal  = "global"
)

// Statuses (§18.2 lifecycle).
const (
	StatusActive  = "active"
	StatusMuted   = "muted"
	StatusExpired = "expired"
)

// Record is one persisted §18.2 CorrectionRecord.
type Record struct {
	ID           string
	ProjectID    string
	JobID        string
	ArtifactID   string
	Category     string
	Severity     string
	Scope        string
	UserFeedback string
	DerivedRule  string
	Status       string
	AppliedCount int
	ContextJSON  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CaptureInput describes a correction to record. Source is required; the
// other fields are optional for machine sources (defaulted from the source
// table) and required for user-driven sources (§18.4).
type CaptureInput struct {
	Source       Source
	ProjectID    string
	JobID        string
	ArtifactID   string
	Scope        string
	Category     string
	Severity     string
	UserFeedback string
	DerivedRule  string
	Detail       string
}

type sourceProfile struct {
	scope, category, severity, rule string
}

// sourceDefaults maps each §18.1 source to the correction it implies. The
// user-driven sources have no defaults: §18.4 requires the full form.
var sourceDefaults = map[Source]sourceProfile{
	SourceUserRejection:    {},
	SourceUserCorrection:   {},
	SourceTestFailure:      {ScopeProject, CategoryTesting, SeverityMedium, "Make the failing test pass before re-submitting."},
	SourceEvaluatorFailure: {ScopeProject, CategoryTesting, SeverityMedium, "Address every acceptance criterion the evaluator reported missing."},
	SourceSecurityScan:     {ScopeGlobal, CategorySecurity, SeverityCritical, "Never introduce the flagged security pattern."},
	SourceRuntimeError:     {ScopeProject, CategoryOther, SeverityMedium, "Handle the runtime error condition before re-running."},
	SourceLoopDetection:    {ScopeProject, CategoryPerformance, SeverityHigh, "Do not repeat the same tool call; change the approach."},
	SourceBudgetExhaustion: {ScopeProject, CategoryTooling, SeverityHigh, "Reduce scope or token usage to stay within budget."},
	SourceHallucinatedPath: {ScopeProject, CategoryDocumentation, SeverityHigh, "Only reference files and symbols that exist."},
}

func validSource(s Source) bool { _, ok := sourceDefaults[s]; return ok }

func validCategory(c string) bool {
	switch c {
	case CategoryArchitecture, CategoryStyle, CategoryTesting, CategorySecurity,
		CategoryPerformance, CategoryTooling, CategoryDocumentation, CategoryOther:
		return true
	default:
		return false
	}
}

func validSeverity(s string) bool {
	switch s {
	case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	default:
		return false
	}
}

func validScope(s string) bool { return s == ScopeProject || s == ScopeGlobal }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Repo persists correction records.
type Repo struct {
	store *store.Store
}

// NewRepo returns a repo backed by s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

const recordColumns = `id, COALESCE(project_id, ''), COALESCE(source_job_id, ''), COALESCE(artifact_id, ''),
	category, severity, scope, user_feedback, derived_rule, status, usage_count, context_json,
	created_at, updated_at`

func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var createdAt, updatedAt string
	if err := row.Scan(&r.ID, &r.ProjectID, &r.JobID, &r.ArtifactID, &r.Category, &r.Severity,
		&r.Scope, &r.UserFeedback, &r.DerivedRule, &r.Status, &r.AppliedCount, &r.ContextJSON,
		&createdAt, &updatedAt); err != nil {
		return Record{}, err
	}
	var err error
	if r.CreatedAt, err = parseTS(createdAt); err != nil {
		return Record{}, err
	}
	if r.UpdatedAt, err = parseTS(updatedAt); err != nil {
		return Record{}, err
	}
	return r, nil
}

func parseTS(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing correction timestamp %q: %w", s, err)
	}
	return t, nil
}

// Capture validates and persists one correction, then audits it under the
// `feedback` category (§28.1).
func (r *Repo) Capture(ctx context.Context, in CaptureInput) (Record, error) {
	if !validSource(in.Source) {
		return Record{}, fmt.Errorf("%w: %q", ErrUnknownSource, in.Source)
	}
	// §18.4: a user rejection/correction must carry the full structured
	// form. Check the raw inputs before defaults can mask a missing field.
	if in.Source == SourceUserRejection || in.Source == SourceUserCorrection {
		if in.Category == "" || in.Severity == "" || in.UserFeedback == "" || in.DerivedRule == "" || in.Scope == "" {
			return Record{}, fmt.Errorf("%w: category, severity, reason, desired behavior, and scope are required", ErrIncompleteFeedback)
		}
	}

	d := sourceDefaults[in.Source]
	scope := firstNonEmpty(in.Scope, d.scope, ScopeProject)
	category := firstNonEmpty(in.Category, d.category, CategoryOther)
	severity := firstNonEmpty(in.Severity, d.severity, SeverityMedium)
	rule := firstNonEmpty(in.DerivedRule, d.rule)
	switch {
	case !validScope(scope):
		return Record{}, fmt.Errorf("%w: scope %q", ErrInvalidField, scope)
	case !validCategory(category):
		return Record{}, fmt.Errorf("%w: category %q", ErrInvalidField, category)
	case !validSeverity(severity):
		return Record{}, fmt.Errorf("%w: severity %q", ErrInvalidField, severity)
	case rule == "":
		return Record{}, fmt.Errorf("%w: derived rule is empty", ErrIncompleteFeedback)
	}

	detail := map[string]any{"source": string(in.Source)}
	if in.Detail != "" {
		detail["detail"] = in.Detail
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return Record{}, fmt.Errorf("marshalling correction context: %w", err)
	}

	id := ids.New()
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO corrections (id, project_id, source_job_id, artifact_id, category, severity, scope, user_feedback, derived_rule, context_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, nullIfEmpty(in.ProjectID), nullIfEmpty(in.JobID), nullIfEmpty(in.ArtifactID),
		category, severity, scope, in.UserFeedback, rule, string(raw),
	); err != nil {
		return Record{}, fmt.Errorf("inserting correction: %w", err)
	}
	_, _ = r.store.AppendEvent(ctx, store.Event{
		Category: "feedback", ProjectID: in.ProjectID, JobID: in.JobID,
		Data: map[string]any{"event": "correction_created", "correction_id": id, "source": string(in.Source), "severity": severity, "scope": scope},
	})
	return r.Get(ctx, id)
}

// Get loads one record by ID.
func (r *Repo) Get(ctx context.Context, id string) (Record, error) {
	row := r.store.DB().QueryRowContext(ctx, `SELECT `+recordColumns+` FROM corrections WHERE id = ?`, id)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Record{}, fmt.Errorf("loading correction: %w", err)
	}
	return rec, nil
}

// Active returns active records, highest severity first then newest.
func (r *Repo) Active(ctx context.Context) ([]Record, error) {
	return r.query(ctx, `WHERE status = 'active' ORDER BY
		CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
		created_at DESC`)
}

// ListByProject returns a project's records, newest first.
func (r *Repo) ListByProject(ctx context.Context, projectID string) ([]Record, error) {
	return r.query(ctx, `WHERE project_id = ? ORDER BY created_at DESC`, projectID)
}

// SetStatus moves a record between active/muted/expired (§18.3).
func (r *Repo) SetStatus(ctx context.Context, id, status string) error {
	if status != StatusActive && status != StatusMuted && status != StatusExpired {
		return fmt.Errorf("%w: status %q", ErrInvalidField, status)
	}
	res, err := r.store.DB().ExecContext(ctx, `UPDATE corrections SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("setting correction status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

// MarkApplied increments the applied count when a correction is injected
// into a prompt (§18.3).
func (r *Repo) MarkApplied(ctx context.Context, id string) error {
	res, err := r.store.DB().ExecContext(ctx, `UPDATE corrections SET usage_count = usage_count + 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("incrementing correction usage: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

func (r *Repo) query(ctx context.Context, tail string, args ...any) ([]Record, error) {
	rows, err := r.store.DB().QueryContext(ctx, `SELECT `+recordColumns+` FROM corrections `+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("listing corrections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning correction: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating corrections: %w", err)
	}
	return out, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
