package strategy

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
)

// TestRecentStats is the F4-T2 familiarity signal: per-archetype outcome
// counts and the accepted-new rate, scoped to the archetype.
func TestRecentStats(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()

	projects := project.NewRepo(s)
	p, task, err := projects.Create(ctx, "recent-demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs := job.NewRepository(s)
	mk := func(result string) {
		t.Helper()
		j, err := jobs.Create(ctx, task.ID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateProfile(ctx, Profile{JobID: j.ID, ProjectID: p.ID, Archetype: "code"}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateOutcome(ctx, Outcome{JobID: j.ID, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	mk(ResultAcceptedNew)
	mk(ResultAcceptedNew)
	mk(ResultRejected)

	samples, rate, err := repo.RecentStats(ctx, "code", 10)
	if err != nil {
		t.Fatal(err)
	}
	if samples != 3 {
		t.Errorf("samples = %d, want 3", samples)
	}
	if rate < 0.66 || rate > 0.67 {
		t.Errorf("accept rate = %v, want 2/3", rate)
	}

	// A different archetype has no history.
	if n, _, err := repo.RecentStats(ctx, "text", 10); err != nil || n != 0 {
		t.Errorf("text stats = %d, %v; want 0, nil", n, err)
	}
}
