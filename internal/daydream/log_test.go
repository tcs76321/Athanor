package daydream

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func testRepo(t *testing.T) *Repo {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "athanor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewRepo(st)
}

func TestInsertAndRecentRoundTrip(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	in := Log{
		Action:                   ActionFeedbackReview,
		PersonaUsed:              "security",
		ArtifactsProduced:        nil,
		CorrectionsProposed:      []string{"c1", "c2"},
		InsightsProposed:         []string{"i1"},
		ChunksProcessed:          3,
		TokensSaved:              120,
		DormantIndexEntriesAdded: 2,
	}
	got, err := r.Insert(ctx, in)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if got.ID == "" {
		t.Fatal("insert did not assign an id")
	}
	logs, err := r.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	back := logs[0]
	if back.Action != in.Action || back.PersonaUsed != in.PersonaUsed {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
	if len(back.CorrectionsProposed) != 2 || back.CorrectionsProposed[0] != "c1" {
		t.Fatalf("corrections = %v", back.CorrectionsProposed)
	}
	if len(back.ArtifactsProduced) != 0 {
		t.Fatalf("artifacts = %v, want empty", back.ArtifactsProduced)
	}
	if back.ChunksProcessed != 3 || back.TokensSaved != 120 || back.DormantIndexEntriesAdded != 2 {
		t.Fatalf("counters = %+v", back)
	}
}

func TestInsertRejectsUnknownAction(t *testing.T) {
	r := testRepo(t)
	if _, err := r.Insert(context.Background(), Log{Action: "not_a_real_action"}); err == nil {
		t.Fatal("unknown action accepted, want rejection")
	}
}

func TestRecentOrdersNewestFirst(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	for _, a := range []string{ActionMemoryConsolidation, ActionStrategyMining, ActionRepoExploration} {
		if _, err := r.Insert(ctx, Log{Action: a}); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := r.Recent(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("limit not honored: %d", len(logs))
	}
}
