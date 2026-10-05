package hitl

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/job"
)

// Enqueuer is the engine surface the service resumes an approved job
// through. *engine.Engine satisfies it; tests pass a fake.
type Enqueuer interface {
	Enqueue(jobID string)
}

// Approver is the approved side effect for a request type that has no linked
// job (e.g. git_push; ADR-0036). It is invoked only on approval, never on
// rejection or expiry.
type Approver func(ctx context.Context, req Request) error

// Service drives the job side of the HITL queue (ADR-0035): Await parks a
// job for a decision, Decide resumes or fails it, and Expire denies overdue
// requests. It also runs registered per-type approvers (ADR-0036).
type Service struct {
	repo       *Repo
	jobs       *job.Repository
	engine     Enqueuer
	DefaultTTL time.Duration
	approvers  map[string]Approver
	// Now is injectable for tests; it defaults to time.Now.
	Now func() time.Time
}

// NewService wires a Service. A non-positive defaultTTL falls back to 24h.
func NewService(repo *Repo, jobs *job.Repository, engine Enqueuer, defaultTTL time.Duration) *Service {
	if defaultTTL <= 0 {
		defaultTTL = 24 * time.Hour
	}
	return &Service{repo: repo, jobs: jobs, engine: engine, DefaultTTL: defaultTTL, Now: time.Now}
}

// SetApprover registers the approved side effect for a request type
// (ADR-0036). A nil fn clears it.
func (s *Service) SetApprover(typ string, fn Approver) {
	if fn == nil {
		delete(s.approvers, typ)
		return
	}
	if s.approvers == nil {
		s.approvers = map[string]Approver{}
	}
	s.approvers[typ] = fn
}

// Await parks a job in awaiting_approval (recording where it came from) and
// creates a pending request. A non-positive ttl uses DefaultTTL. Calling
// Await on a job already awaiting creates a second request; callers should
// only do so for a distinct approval.
func (s *Service) Await(ctx context.Context, jobID, typ, severity, summary string, details map[string]any, ttl time.Duration) (Request, error) {
	j, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return Request{}, err
	}
	if j.State != job.StateAwaitingApproval {
		if _, err := s.jobs.Transition(ctx, jobID, job.StateAwaitingApproval); err != nil {
			return Request{}, err
		}
	}
	if ttl <= 0 {
		ttl = s.DefaultTTL
	}
	expires := s.Now().Add(ttl)
	payload := map[string]any{"summary": summary, "resume_state": string(j.State)}
	for k, v := range details {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Request{}, err
	}
	return s.repo.Create(ctx, Request{
		ProjectID: j.ProjectID, JobID: jobID, Type: typ, Severity: severity,
		PayloadJSON: string(raw), ExpiresAt: &expires,
	})
}

// Pending lists pending requests, oldest first.
func (s *Service) Pending(ctx context.Context) ([]Request, error) { return s.repo.Pending(ctx) }

// List lists every request, newest first (limit <= 0 means no limit).
func (s *Service) List(ctx context.Context, limit int) ([]Request, error) {
	return s.repo.List(ctx, limit)
}

// Get loads one request.
func (s *Service) Get(ctx context.Context, id string) (Request, error) { return s.repo.Get(ctx, id) }

// Decide applies a decision and drives the linked job: approve resumes it to
// the state it left, reject fails it. Defer only extends the window. On
// approval, a registered per-type approver runs (ADR-0036).
func (s *Service) Decide(ctx context.Context, id, action, note string, deferFor time.Duration) (Request, error) {
	req, err := s.repo.Decide(ctx, id, action, note, deferFor, s.Now())
	if err != nil {
		return req, err
	}
	switch req.Status {
	case StatusApproved:
		s.applyOutcome(ctx, req)
		s.runApprover(ctx, req)
	case StatusRejected:
		s.applyOutcome(ctx, req)
	}
	return req, nil
}

// runApprover invokes the request type's approved side effect, if any. A
// failure is logged (the decision already stands); the approver audits its
// own outcome so a post-mortem sees it.
func (s *Service) runApprover(ctx context.Context, req Request) {
	fn, ok := s.approvers[req.Type]
	if !ok {
		return
	}
	if err := fn(ctx, req); err != nil {
		slog.Error("hitl: approved action failed", "request", req.ID, "type", req.Type, "err", err)
	}
}

// Expire denies every overdue request and fails its job; it returns how many
// were expired.
func (s *Service) Expire(ctx context.Context) (int, error) {
	expired, err := s.repo.ExpireOverdue(ctx, s.Now())
	if err != nil {
		return 0, err
	}
	for _, req := range expired {
		s.applyOutcome(ctx, req)
	}
	return len(expired), nil
}

// applyOutcome transitions the linked job for a resolved request.
func (s *Service) applyOutcome(ctx context.Context, req Request) {
	if req.JobID == "" {
		return
	}
	j, err := s.jobs.Get(ctx, req.JobID)
	if err != nil {
		slog.Error("hitl: loading job for decision", "request", req.ID, "job", req.JobID, "err", err)
		return
	}
	if j.State != job.StateAwaitingApproval {
		return // already resolved, or not waiting
	}
	var to job.State
	switch req.Status {
	case StatusApproved:
		to = j.AwaitingFrom
		if to == "" {
			slog.Error("hitl: cannot approve a job with no awaiting_from", "job", req.JobID)
			return
		}
	case StatusRejected, StatusExpired:
		to = job.StateFailed
	default:
		return
	}
	if _, err := s.jobs.Transition(ctx, req.JobID, to); err != nil {
		slog.Error("hitl: applying decision to job", "request", req.ID, "job", req.JobID, "to", to, "err", err)
		return
	}
	if req.Status == StatusApproved && s.engine != nil {
		s.engine.Enqueue(req.JobID)
	}
}
