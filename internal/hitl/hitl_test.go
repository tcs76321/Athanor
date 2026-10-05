package hitl

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func openHitl(t *testing.T) (*Repo, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return NewRepo(s), s
}

func TestCreateGetAndAudit(t *testing.T) {
	repo, s := openHitl(t)
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	req, err := repo.Create(ctx, Request{
		Type: TypeTaskEscalation, Severity: SeverityHigh,
		PayloadJSON: `{"reason":"blocked"}`, ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.ID == "" || req.Status != StatusPending || req.Severity != SeverityHigh {
		t.Fatalf("request = %+v", req)
	}
	got, err := repo.Get(ctx, req.ID)
	if err != nil || got.ID != req.ID {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	events, err := s.QueryEvents(ctx, store.EventFilter{Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 request event", len(events))
	}
}

func TestDecideApproveRejectDefer(t *testing.T) {
	repo, _ := openHitl(t)
	ctx := context.Background()
	now := time.Now()

	mk := func() Request {
		t.Helper()
		r, err := repo.Create(ctx, Request{Type: TypeTaskEscalation})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// approve
	a := mk()
	decided, err := repo.Decide(ctx, a.ID, ActionApprove, "ok", 0, now)
	if err != nil || decided.Status != StatusApproved || decided.DecidedAt == nil {
		t.Fatalf("approve = %+v, %v", decided, err)
	}
	if _, err := repo.Decide(ctx, a.ID, ActionApprove, "again", 0, now); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second decision err = %v, want ErrNotPending", err)
	}

	// reject
	rj := mk()
	if _, err := repo.Decide(ctx, rj.ID, ActionReject, "no", 0, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, rj.ID); got.Status != StatusRejected {
		t.Errorf("reject status = %q, want rejected", got.Status)
	}

	// defer keeps pending and extends the window
	exp := now.Add(time.Hour)
	df, err := repo.Create(ctx, Request{Type: TypeTaskEscalation, ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Decide(ctx, df.ID, ActionDefer, "later", 2*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending || got.ExpiresAt == nil || !got.ExpiresAt.After(exp) {
		t.Errorf("defer = %+v, want pending with a later expiry", got)
	}

	// unknown action
	if _, err := repo.Decide(ctx, mk().ID, "maybe", "", 0, now); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("unknown action err = %v, want ErrUnknownAction", err)
	}
}

func TestExpireOverdue(t *testing.T) {
	repo, _ := openHitl(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	old, _ := repo.Create(ctx, Request{Type: TypeTaskEscalation, ExpiresAt: &past})
	fresh, _ := repo.Create(ctx, Request{Type: TypeTaskEscalation, ExpiresAt: &future})
	none, _ := repo.Create(ctx, Request{Type: TypeTaskEscalation})

	expired, err := repo.ExpireOverdue(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != old.ID {
		t.Fatalf("expired = %+v, want only %s", expired, old.ID)
	}
	if got, _ := repo.Get(ctx, old.ID); got.Status != StatusExpired {
		t.Errorf("old status = %q, want expired", got.Status)
	}
	if got, _ := repo.Get(ctx, fresh.ID); got.Status != StatusPending {
		t.Errorf("fresh status = %q, want pending", got.Status)
	}
	if got, _ := repo.Get(ctx, none.ID); got.Status != StatusPending {
		t.Errorf("no-expiry status = %q, want pending", got.Status)
	}
}

// --- Service tests ---

type fakeEnqueuer struct{ jobs []string }

func (f *fakeEnqueuer) Enqueue(id string) { f.jobs = append(f.jobs, id) }

func serviceFixture(t *testing.T) (*Service, *job.Repository, *fakeEnqueuer, string) {
	t.Helper()
	repo, s := openHitl(t)
	projects := project.NewRepo(s)
	jobs := job.NewRepository(s)
	ctx := context.Background()
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeText,
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := jobs.Create(ctx, task.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Transition(ctx, j.ID, job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}
	eng := &fakeEnqueuer{}
	svc := NewService(repo, jobs, eng, time.Hour)
	return svc, jobs, eng, j.ID
}

func TestServiceApproveResumesJob(t *testing.T) {
	svc, jobs, eng, jobID := serviceFixture(t)
	ctx := context.Background()

	req, err := svc.Await(ctx, jobID, TypeTaskEscalation, SeverityHigh, "please approve", nil, 0)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if j, _ := jobs.Get(ctx, jobID); j.State != job.StateAwaitingApproval || j.AwaitingFrom != job.StateContextBuilding {
		t.Fatalf("job = %+v, want awaiting_approval from context_building", j)
	}
	if _, err := svc.Decide(ctx, req.ID, ActionApprove, "ok", 0); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	j, _ := jobs.Get(ctx, jobID)
	if j.State != job.StateContextBuilding {
		t.Errorf("job state = %s, want context_building (resumed)", j.State)
	}
	if j.AwaitingFrom != "" {
		t.Errorf("awaiting_from not cleared: %q", j.AwaitingFrom)
	}
	if len(eng.jobs) != 1 || eng.jobs[0] != jobID {
		t.Errorf("enqueued = %v, want [%s]", eng.jobs, jobID)
	}
}

func TestServiceRejectFailsJob(t *testing.T) {
	svc, jobs, eng, jobID := serviceFixture(t)
	ctx := context.Background()
	req, err := svc.Await(ctx, jobID, TypeTaskEscalation, SeverityHigh, "s", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Decide(ctx, req.ID, ActionReject, "no", 0); err != nil {
		t.Fatal(err)
	}
	if j, _ := jobs.Get(ctx, jobID); j.State != job.StateFailed {
		t.Errorf("job state = %s, want failed", j.State)
	}
	if len(eng.jobs) != 0 {
		t.Errorf("rejected job should not be enqueued: %v", eng.jobs)
	}
}

func TestServiceExpiryDeniesByDefault(t *testing.T) {
	svc, jobs, _, jobID := serviceFixture(t)
	ctx := context.Background()
	base := time.Now()
	svc.Now = func() time.Time { return base }
	if _, err := svc.Await(ctx, jobID, TypeTaskEscalation, SeverityHigh, "s", nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	svc.Now = func() time.Time { return base.Add(2 * time.Minute) }
	n, err := svc.Expire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired = %d, want 1", n)
	}
	if j, _ := jobs.Get(ctx, jobID); j.State != job.StateFailed {
		t.Errorf("job state = %s, want failed (expiry denies)", j.State)
	}
}
