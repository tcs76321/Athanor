package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestHitlEscalatorCreatesRequest(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	repo := hitl.NewRepo(s)
	projects := project.NewRepo(s)
	ctx := context.Background()
	_, task, err := projects.Create(ctx, "demo", project.ArchetypeText,
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := (hitlEscalator{repo: repo}).Escalate(ctx, task, "retries_exhausted"); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	pending, err := repo.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if pending[0].Type != hitl.TypeTaskEscalation || pending[0].Severity != hitl.SeverityHigh {
		t.Errorf("request = %+v", pending[0])
	}
	if pending[0].JobID != "" {
		t.Errorf("task escalation should not be job-linked: %q", pending[0].JobID)
	}
}
