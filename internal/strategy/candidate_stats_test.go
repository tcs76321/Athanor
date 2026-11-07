package strategy

import (
	"context"
	"fmt"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

func createNamedJob(t *testing.T, s *store.Store, name string) (projectID, jobID string) {
	t.Helper()
	ctx := context.Background()
	projects := project.NewRepo(s)
	p, task, err := projects.Create(ctx, name, project.ArchetypeCode,
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

func TestCandidateStatsGroupsByDivergenceArm(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()

	for i, tc := range []struct {
		candidates int
		result     string
	}{
		{3, ResultAcceptedNew},
		{3, ResultRejected},
		{5, ResultAcceptedNew},
		{5, ResultAcceptedNew},
	} {
		projectID, jobID := createNamedJob(t, s, fmt.Sprintf("candidate-demo-%d", i))
		if _, err := repo.CreateProfile(ctx, Profile{
			JobID: jobID, ProjectID: projectID, Archetype: "code",
			Signature: []SignatureEntry{
				{Phase: "diverging", Persona: "main", Candidates: tc.candidates},
			},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: tc.result}); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := repo.CandidateStats(ctx, "code", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want two arms", stats)
	}
	if stats[0].Candidates != 3 || stats[0].Samples != 2 || stats[0].Accepted != 1 {
		t.Errorf("arm 3 = %+v", stats[0])
	}
	if stats[1].Candidates != 5 || stats[1].Samples != 2 || stats[1].Accepted != 2 {
		t.Errorf("arm 5 = %+v", stats[1])
	}
}

// TestCandidateStatsUsesActualOutcomeArm proves a one-shot/panel-capped run is
// learned under the arm that actually ran, not the planned count in the
// profile.
func TestCandidateStatsUsesActualOutcomeArm(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	projectID, jobID := createNamedJob(t, s, "candidate-actual")
	if _, err := repo.CreateProfile(ctx, Profile{
		JobID: jobID, ProjectID: projectID, Archetype: "code",
		Signature: []SignatureEntry{{Phase: "diverging", Persona: "main", Candidates: 10}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateOutcome(ctx, Outcome{
		JobID: jobID, Result: ResultAcceptedNew, Candidates: 1,
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := repo.CandidateStats(ctx, "code", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Candidates != 1 || stats[0].Samples != 1 {
		t.Fatalf("stats = %+v, want the actual arm 1", stats)
	}
}

func TestCandidateStatsIgnoresProfilesWithoutDivergence(t *testing.T) {
	repo, s := openStrategy(t)
	ctx := context.Background()
	projectID, jobID := createJob(t, s)
	if _, err := repo.CreateProfile(ctx, Profile{
		JobID: jobID, ProjectID: projectID, Archetype: "code",
		Signature: []SignatureEntry{{Phase: "planning", Persona: "tall"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateOutcome(ctx, Outcome{JobID: jobID, Result: ResultAcceptedNew}); err != nil {
		t.Fatal(err)
	}
	stats, err := repo.CandidateStats(ctx, "code", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("stats = %+v, want none", stats)
	}
}
