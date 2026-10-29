package review

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func testRepo(t *testing.T) (*Repo, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`
		INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','code','build a thing');
		INSERT INTO artifacts (id, project_id, kind, status) VALUES
			('a1','p1','code','accepted'),
			('a2','p1','code','accepted');
	`); err != nil {
		t.Fatal(err)
	}
	return NewRepo(st), st
}

func TestReviewQueueAndCreate(t *testing.T) {
	r, _ := testRepo(t)
	ctx := context.Background()

	q, err := r.Queue(ctx, 10)
	if err != nil || len(q) != 2 {
		t.Fatalf("queue = %v, %v; want two accepted artifacts", q, err)
	}

	rv, err := r.Create(ctx, CreateInput{
		ArtifactID: "a1", JobID: "j1", ProjectID: "p1",
		Score: 0.8, Accepted: true, Blinded: true,
		Rubric: map[string]int{"clarity": 4}, Note: "good",
		Provenance: map[string]string{"model": "m1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rv.ID == "" || !rv.Accepted || rv.Score != 0.8 || rv.Rubric["clarity"] != 4 {
		t.Errorf("review = %+v", rv)
	}
	got, err := r.ListByArtifact(ctx, "a1")
	if err != nil || len(got) != 1 || got[0].ID != rv.ID {
		t.Fatalf("list = %+v, %v; want the review", got, err)
	}
	q, err = r.Queue(ctx, 10)
	if err != nil || len(q) != 1 || q[0] != "a2" {
		t.Errorf("queue after review = %v, %v; want only a2", q, err)
	}
}

func TestReviewRejectsBadScore(t *testing.T) {
	r, _ := testRepo(t)
	if _, err := r.Create(context.Background(), CreateInput{
		ArtifactID: "a1", JobID: "j1", ProjectID: "p1", Score: 1.5,
	}); err == nil {
		t.Error("out-of-range score accepted")
	}
}
