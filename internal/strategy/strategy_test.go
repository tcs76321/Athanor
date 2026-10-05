package strategy

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

func openStrategy(t *testing.T) (*Repo, *store.Store) {
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

func createJob(t *testing.T, s *store.Store) (projectID, jobID string) {
	t.Helper()
	ctx := context.Background()
	projects := project.NewRepo(s)
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := job.NewRepository(s).Create(ctx, task.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID, j.ID
}

func TestProfileAndOutcomeRoundTrip(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	projectID, jobID := createJob(t, s)

	profile, err := repo.CreateProfile(ctx, Profile{
		JobID: jobID, ProjectID: projectID, Archetype: "code",
		Signature: []SignatureEntry{
			{Phase: "planning", Persona: "tall", Temperature: 0.2},
			{Phase: "diverging", Persona: "main", Temperature: 0.7, Candidates: 3},
		},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if profile.ID == "" || len(profile.Signature) != 2 {
		t.Fatalf("profile = %+v", profile)
	}
	// Idempotent.
	again, err := repo.CreateProfile(ctx, Profile{JobID: jobID, ProjectID: projectID, Archetype: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != profile.ID || again.Archetype != "code" {
		t.Errorf("idempotent CreateProfile changed the record: %+v", again)
	}

	outcome, err := repo.CreateOutcome(ctx, Outcome{
		JobID: jobID, Result: ResultAcceptedNew, Score: 0.9, EvaluatorConfidence: 0.8,
		TokenCost: 1234, WallTime: 90 * time.Second,
	})
	if err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}
	if outcome.StrategyProfileID != profile.ID || outcome.Result != ResultAcceptedNew || outcome.WallTime != 90*time.Second {
		t.Fatalf("outcome = %+v", outcome)
	}
	// Idempotent.
	if _, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: ResultFailed}); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetOutcomeByJob(ctx, jobID); got.Result != ResultAcceptedNew {
		t.Errorf("second outcome overwrote the first: %+v", got)
	}
}

func TestOutcomeRequiresProfile(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	_, jobID := createJob(t, s)
	if _, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: ResultFailed}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (no profile)", err)
	}
	if _, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: "bogus"}); err == nil {
		t.Fatal("invalid result accepted")
	}
}

func TestBackfillTerminalJobs(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	projectID, _ := createJob(t, s)
	// A legacy terminal job inserted directly (no profile captured).
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO jobs (id, task_id, project_id, state) SELECT 'legacy1', id, project_id, 'completed' FROM tasks LIMIT 1`,
	); err != nil {
		t.Fatal(err)
	}

	n, err := repo.Backfill(ctx)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfilled = %d, want 1", n)
	}
	if _, err := repo.GetProfileByJob(ctx, "legacy1"); err != nil {
		t.Errorf("no profile for legacy job: %v", err)
	}
	o, err := repo.GetOutcomeByJob(ctx, "legacy1")
	if err != nil || o.Result != ResultAcceptedNew {
		t.Errorf("outcome = %+v, err %v", o, err)
	}
	// Rerun is a no-op.
	if n, err := repo.Backfill(ctx); err != nil || n != 0 {
		t.Errorf("second backfill = %d, %v; want 0", n, err)
	}
	_ = projectID
}
