// Package hitl implements the ARCHITECTURE §20 human-in-the-loop request
// queue (ROADMAP M6-T4; ADR-0035): persisted requests, decisions, expiry,
// and the job pause/resume they drive.
//
// Repo owns the hitl_requests table; Service adds the job interaction
// (entering awaiting_approval, resuming on approval, failing on rejection or
// expiry). Every request and decision appends an audit event under the
// `jobs` category.
package hitl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// ErrNotFound reports a request ID that does not exist.
var ErrNotFound = errors.New("hitl: request not found")

// ErrNotPending reports a decision on a request that is already resolved.
var ErrNotPending = errors.New("hitl: request is not pending")

// ErrUnknownAction reports a decision verb outside the closed set.
var ErrUnknownAction = errors.New("hitl: unknown decision action")

// Severities (§20.2). The set is closed by the schema CHECK.
const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// Statuses (§20.2). The set is closed by the schema CHECK.
const (
	StatusPending   = "pending"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"
)

// Decision actions (§20.1).
const (
	ActionApprove = "approve"
	ActionReject  = "reject"
	ActionDefer   = "defer"
)

// Request types (a subset of §20.1; the column is open text).
const (
	TypeTaskEscalation = "task_escalation"
	TypeGitPush        = "git_push"
)

// Request is one §20.2 HITL request.
type Request struct {
	ID           string
	ProjectID    string
	JobID        string
	Type         string
	Severity     string
	Status       string
	PayloadJSON  string
	DecisionNote string
	ExpiresAt    *time.Time
	DecidedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Repo persists HITL requests.
type Repo struct {
	store *store.Store
}

// NewRepo returns a repo backed by s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

const requestColumns = `id, COALESCE(project_id, ''), COALESCE(job_id, ''), type, severity,
	status, payload_json, COALESCE(decision_note, ''), expires_at, decided_at, created_at, updated_at`

func scanRequest(row interface{ Scan(...any) error }) (Request, error) {
	var r Request
	var expiresAt, decidedAt sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(&r.ID, &r.ProjectID, &r.JobID, &r.Type, &r.Severity, &r.Status,
		&r.PayloadJSON, &r.DecisionNote, &expiresAt, &decidedAt, &createdAt, &updatedAt); err != nil {
		return Request{}, err
	}
	var err error
	if r.CreatedAt, err = parseTS(createdAt); err != nil {
		return Request{}, err
	}
	if r.UpdatedAt, err = parseTS(updatedAt); err != nil {
		return Request{}, err
	}
	if expiresAt.Valid {
		t, err := parseTS(expiresAt.String)
		if err != nil {
			return Request{}, err
		}
		r.ExpiresAt = &t
	}
	if decidedAt.Valid {
		t, err := parseTS(decidedAt.String)
		if err != nil {
			return Request{}, err
		}
		r.DecidedAt = &t
	}
	return r, nil
}

func parseTS(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing hitl timestamp %q: %w", s, err)
	}
	return t, nil
}

// Create inserts a pending request and audits it.
func (r *Repo) Create(ctx context.Context, req Request) (Request, error) {
	if req.Type == "" {
		return Request{}, fmt.Errorf("hitl: request type must not be empty")
	}
	if req.Severity == "" {
		req.Severity = SeverityMedium
	}
	id := ids.New()
	var expiresAt any
	if req.ExpiresAt != nil {
		expiresAt = req.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if req.PayloadJSON == "" {
		req.PayloadJSON = "{}"
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO hitl_requests (id, project_id, job_id, type, severity, status, payload_json, expires_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`,
		id, nullIfEmpty(req.ProjectID), nullIfEmpty(req.JobID), req.Type, req.Severity, req.PayloadJSON, expiresAt,
	); err != nil {
		return Request{}, fmt.Errorf("inserting hitl request: %w", err)
	}
	r.audit(ctx, req.ProjectID, req.JobID, map[string]any{
		"event": "hitl_request", "request_id": id, "type": req.Type, "severity": req.Severity,
	})
	return r.Get(ctx, id)
}

// Get loads one request by ID.
func (r *Repo) Get(ctx context.Context, id string) (Request, error) {
	row := r.store.DB().QueryRowContext(ctx, `SELECT `+requestColumns+` FROM hitl_requests WHERE id = ?`, id)
	req, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Request{}, fmt.Errorf("loading hitl request: %w", err)
	}
	return req, nil
}

// Pending returns every pending request, oldest first.
func (r *Repo) Pending(ctx context.Context) ([]Request, error) {
	return r.query(ctx, `WHERE status = 'pending' ORDER BY created_at ASC, id ASC`)
}

// List returns every request, newest first, up to limit (<=0 means no limit).
func (r *Repo) List(ctx context.Context, limit int) ([]Request, error) {
	q := `ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	return r.query(ctx, q)
}

func (r *Repo) query(ctx context.Context, tail string) ([]Request, error) {
	rows, err := r.store.DB().QueryContext(ctx, `SELECT `+requestColumns+` FROM hitl_requests `+tail)
	if err != nil {
		return nil, fmt.Errorf("listing hitl requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Request
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning hitl request: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hitl requests: %w", err)
	}
	return out, nil
}

// Decide applies approve/reject/defer to a pending request. `deferFor` must
// be positive for defer and is ignored otherwise. A decision is audited.
func (r *Repo) Decide(ctx context.Context, id, action, note string, deferFor time.Duration, now time.Time) (Request, error) {
	req, err := r.Get(ctx, id)
	if err != nil {
		return Request{}, err
	}
	if req.Status != StatusPending {
		return Request{}, fmt.Errorf("%w: %s is %s", ErrNotPending, id, req.Status)
	}

	var (
		status  string
		decided any
		expires any
	)
	switch action {
	case ActionApprove:
		status, decided = StatusApproved, now.UTC().Format(time.RFC3339Nano)
	case ActionReject:
		status, decided = StatusRejected, now.UTC().Format(time.RFC3339Nano)
	case ActionDefer:
		if deferFor <= 0 {
			return Request{}, fmt.Errorf("hitl: defer requires a positive duration")
		}
		status = StatusPending
		expires = now.Add(deferFor).UTC().Format(time.RFC3339Nano)
		if req.ExpiresAt != nil {
			// Never shorten a deferred window: extend from the later of
			// now and the current expiry.
			base := now
			if req.ExpiresAt.After(base) {
				base = *req.ExpiresAt
			}
			expires = base.Add(deferFor).UTC().Format(time.RFC3339Nano)
		}
	default:
		return Request{}, fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}

	if _, err := r.store.DB().ExecContext(ctx,
		`UPDATE hitl_requests SET status = ?, decision_note = ?, decided_at = ?, expires_at = COALESCE(?, expires_at) WHERE id = ?`,
		status, nullIfEmpty(note), decided, expires, id,
	); err != nil {
		return Request{}, fmt.Errorf("deciding hitl request: %w", err)
	}
	r.audit(ctx, req.ProjectID, req.JobID, map[string]any{
		"event": "hitl_decision", "request_id": id, "action": action, "status": status,
	})
	return r.Get(ctx, id)
}

// ExpireOverdue marks every pending request whose expiry has passed as
// expired, returns them, and audits each. Expiry denies (never approves).
func (r *Repo) ExpireOverdue(ctx context.Context, now time.Time) ([]Request, error) {
	pending, err := r.Pending(ctx)
	if err != nil {
		return nil, err
	}
	var expired []Request
	for _, req := range pending {
		if req.ExpiresAt == nil || req.ExpiresAt.After(now) {
			continue
		}
		if _, err := r.store.DB().ExecContext(ctx,
			`UPDATE hitl_requests SET status = 'expired', decided_at = ? WHERE id = ? AND status = 'pending'`,
			now.UTC().Format(time.RFC3339Nano), req.ID,
		); err != nil {
			return nil, fmt.Errorf("expiring hitl request %s: %w", req.ID, err)
		}
		r.audit(ctx, req.ProjectID, req.JobID, map[string]any{
			"event": "hitl_decision", "request_id": req.ID, "action": "expire", "status": StatusExpired,
		})
		req.Status = StatusExpired
		expired = append(expired, req)
	}
	return expired, nil
}

func (r *Repo) audit(ctx context.Context, projectID, jobID string, data map[string]any) {
	_, _ = r.store.AppendEvent(ctx, store.Event{
		Category: "jobs", ProjectID: projectID, JobID: jobID, Data: data,
	})
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
