package interruptions

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestQueueAndInject(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	projects := project.NewRepo(s)
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeText,
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := job.NewRepository(s).Create(ctx, task.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}

	repo := NewRepo(s)
	n, err := repo.Add(ctx, j.ID, "prefer shorter sentences")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if n.Status != StatusPending || n.Text != "prefer shorter sentences" {
		t.Fatalf("note = %+v", n)
	}

	pending, err := repo.Pending(ctx, j.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("Pending = %v, %v", pending, err)
	}
	if err := repo.MarkInjected(ctx, []string{n.ID}); err != nil {
		t.Fatalf("MarkInjected: %v", err)
	}
	if pending, _ := repo.Pending(ctx, j.ID); len(pending) != 0 {
		t.Errorf("note still pending after injection")
	}
	listed, _ := repo.List(ctx, j.ID)
	if len(listed) != 1 || listed[0].Status != StatusInjected || listed[0].InjectedAt == nil {
		t.Errorf("listed = %+v", listed)
	}

	events, err := s.QueryEvents(ctx, store.EventFilter{Category: "jobs", JobID: j.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Errorf("expected an interruption_queued audit event")
	}
}
