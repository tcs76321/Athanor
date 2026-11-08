package jobstate

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

// TestClearCoversEveryKey is the guard the hand-maintained key list never had:
// every accessor writes a row, and Clear removes all of them. A new accessor
// that forgets Clear fails here instead of leaking one row per finished job.
func TestClearCoversEveryKey(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const job = "job-1"

	if err := s.SetDifficulty(ctx, job, "hard"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLanguage(ctx, job, "go"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnalysis(ctx, job, analysis.Analysis{Units: []analysis.Unit{{ID: "u1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReflectCount(ctx, job, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReflectionProposal(ctx, job, "try another approach"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRefineCount(ctx, job, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRegulatorSignature(ctx, job, "sig"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRefinedCounts(ctx, job, map[string]int{"u1": 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEvictedTiers(ctx, job, []prompt.Tier{prompt.TierCorrections}); err != nil {
		t.Fatal(err)
	}

	s.Clear(ctx, job)

	if got := s.Difficulty(ctx, job); got != "" {
		t.Errorf("difficulty survived Clear: %q", got)
	}
	if got := s.Language(ctx, job); got != "" {
		t.Errorf("language survived Clear: %q", got)
	}
	if _, ok := s.Analysis(ctx, job); ok {
		t.Error("analysis survived Clear")
	}
	if got := s.ReflectCount(ctx, job); got != 0 {
		t.Errorf("reflect counter survived Clear: %d", got)
	}
	if got := s.ReflectionProposal(ctx, job); got != "" {
		t.Errorf("reflection proposal survived Clear: %q", got)
	}
	if got := s.RefineCount(ctx, job); got != 0 {
		t.Errorf("refine counter survived Clear: %d", got)
	}
	if got := s.RegulatorSignature(ctx, job); got != "" {
		t.Errorf("regulator signature survived Clear: %q", got)
	}
	if got := s.RefinedCounts(ctx, job); len(got) != 0 {
		t.Errorf("refined counts survived Clear: %v", got)
	}
	if got := s.EvictedTiers(ctx, job); len(got) != 0 {
		t.Errorf("evicted tiers survived Clear: %v", got)
	}

	var rows int
	if err := s.db.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM system_state WHERE key LIKE '%' || ?`,
		"job-1").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d system_state rows remain for the job", rows)
	}
}

func TestRoundTrips(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const job = "job-2"

	if err := s.SetRefinedCounts(ctx, job, map[string]int{"u1": 3}); err != nil {
		t.Fatal(err)
	}
	if got := s.RefinedCounts(ctx, job); got["u1"] != 3 {
		t.Errorf("refined counts = %v, want u1=3", got)
	}
	if err := s.SetEvictedTiers(ctx, job, []prompt.Tier{prompt.TierEpisodic, prompt.TierCorrections}); err != nil {
		t.Fatal(err)
	}
	got := s.EvictedTiers(ctx, job)
	if len(got) != 2 || got[0] != prompt.TierEpisodic || got[1] != prompt.TierCorrections {
		t.Errorf("evicted tiers = %v", got)
	}
	if err := s.SetEvictedTiers(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.EvictedTiers(ctx, job); len(got) != 0 {
		t.Errorf("evicted tiers after delete = %v", got)
	}
}
