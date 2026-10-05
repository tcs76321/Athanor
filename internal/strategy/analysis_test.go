package strategy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// seedCorpus creates n jobs under one code project, each with a profile whose
// diverging persona is `persona`, and an outcome whose acceptance is given by
// accepted. All confidences are equal so the directionally-consistent check
// passes for both polarities.
func seedCorpus(t *testing.T, s *store.Store, p project.Project, taskID, persona string, n int, accepted int) {
	t.Helper()
	ctx := context.Background()
	repo := NewRepo(s)
	jobs := job.NewRepository(s)
	for i := 0; i < n; i++ {
		j, err := jobs.Create(ctx, taskID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateProfile(ctx, Profile{
			JobID: j.ID, ProjectID: p.ID, Archetype: p.Archetype,
			Signature: []SignatureEntry{
				{Phase: "planning", Persona: "tall", Temperature: 0.2},
				{Phase: "diverging", Persona: persona, Temperature: 0.8, Candidates: 3},
			},
		}); err != nil {
			t.Fatal(err)
		}
		result := ResultRejected
		if i < accepted {
			result = ResultAcceptedNew
		}
		if _, err := repo.CreateOutcome(ctx, Outcome{
			JobID: j.ID, Result: result, EvaluatorConfidence: 0.8, Score: 0.5,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMineSyntheticCorpus(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	projects := project.NewRepo(s)
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// alternative: 16/20 accepted (0.80); main: 8/20 (0.40). Baseline 0.60.
	seedCorpus(t, s, p, task.ID, "alternative", 20, 16)
	seedCorpus(t, s, p, task.ID, "main", 20, 8)

	repo := NewRepo(s)
	proposed, err := repo.Mine(ctx, MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.15})
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	byPersona := map[string]Insight{}
	for _, ins := range proposed {
		if ins.Pattern.Feature == "diverging.persona" {
			byPersona[ins.Pattern.Value] = ins
		}
	}
	alt, ok := byPersona["alternative"]
	if !ok || alt.Polarity != PolarityWinning || alt.Evidence.CohortJobs != 20 {
		t.Fatalf("alternative insight = %+v (ok=%v)", alt, ok)
	}
	mainIns, ok := byPersona["main"]
	if !ok || mainIns.Polarity != PolarityLosing {
		t.Fatalf("main insight = %+v (ok=%v)", mainIns, ok)
	}
	if alt.Evidence.AcceptRate < 0.79 || alt.Evidence.AcceptRate > 0.81 {
		t.Errorf("alternative accept rate = %v, want ~0.80", alt.Evidence.AcceptRate)
	}

	// Proposed insights are inert: no active statements.
	if stmts, _ := repo.ActiveStatements(ctx); len(stmts) != 0 {
		t.Fatalf("proposed insights leaked into active statements: %v", stmts)
	}

	// Promotion is explicit; re-mining does not duplicate.
	if err := repo.SetInsightStatus(ctx, alt.ID, InsightActive); err != nil {
		t.Fatal(err)
	}
	stmts, err := repo.ActiveStatements(ctx)
	if err != nil || len(stmts) != 1 {
		t.Fatalf("active statements = %v (err %v), want 1", stmts, err)
	}
	again, err := repo.Mine(ctx, MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.15})
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range again {
		if ins.Pattern.Feature == "diverging.persona" && ins.Pattern.Value == "alternative" {
			t.Errorf("re-mining duplicated an existing insight: %+v", ins)
		}
	}
}

func TestMineRespectsCohortFloor(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	projects := project.NewRepo(s)
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCorpus(t, s, p, task.ID, "alternative", 5, 5)
	if proposed, err := NewRepo(s).Mine(ctx, MineOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.1}); err != nil || len(proposed) != 0 {
		t.Fatalf("below-floor corpus produced %d insights (err %v)", len(proposed), err)
	}
	if _, err := NewRepo(s).Mine(ctx, MineOptions{MinCohortSize: 0}); err == nil {
		t.Error("zero cohort floor accepted")
	}
}
