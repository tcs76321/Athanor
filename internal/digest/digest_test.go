package digest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "athanor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func TestBuildAggregatesWindow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','proj','code','build things that last')`)
	exec(`INSERT INTO tasks (id, project_id, title, status) VALUES ('t1','p1','do work','completed')`)
	exec(`INSERT INTO goals (id, project_id, text, status) VALUES ('g1','p1','a goal long enough to be valid','completed')`)
	exec(`INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j1','t1','p1','completed')`)
	exec(`INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j2','t1','p1','failed')`)
	if _, err := st.AppendEvent(ctx, store.Event{Category: "jobs", JobID: "j2", ProjectID: "p1",
		Data: map[string]any{"event": "job_failed", "error": "pytest exited 1"}}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO artifacts (id, project_id, kind, status) VALUES ('a1','p1','document','draft')`)
	exec(`INSERT INTO artifacts (id, project_id, kind, status) VALUES ('a2','p1','code','accepted')`)
	exec(`INSERT INTO hitl_requests (id, project_id, job_id, type, severity, status) VALUES ('h1','p1','j1','git_push','medium','pending')`)
	exec(`INSERT INTO alarms (id, category, level, message) VALUES ('al1','quality','warning','high rejection')`)
	now := time.Now().UTC().Format(time.RFC3339)
	exec(`INSERT INTO daydream_logs (id, action, persona_used, started_at, finished_at, artifacts_json, corrections_json, insights_json, chunks_processed, tokens_saved)
	      VALUES ('d1','memory_consolidation','security',?,?, '["x"]','[]','["i"]',3,120)`, now, now)

	since := time.Now().Add(-time.Hour)
	d, err := Build(ctx, st, since, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d.JobsCompleted != 1 || d.JobsFailed != 1 {
		t.Fatalf("jobs = completed:%d failed:%d, want 1/1", d.JobsCompleted, d.JobsFailed)
	}
	if len(d.Failures) != 1 || d.Failures[0].Reason != "pytest exited 1" {
		t.Fatalf("failures = %+v, want j2 with reason", d.Failures)
	}
	if d.GoalsCompleted != 1 || d.TasksDone != 1 {
		t.Fatalf("goals/tasks = %d/%d, want 1/1", d.GoalsCompleted, d.TasksDone)
	}
	if d.Artifacts.Draft != 1 || d.Artifacts.Accepted != 1 {
		t.Fatalf("artifacts = %+v, want 1 draft + 1 accepted", d.Artifacts)
	}
	if d.PendingHITL != 1 {
		t.Fatalf("pending hitl = %d, want 1", d.PendingHITL)
	}
	if d.ActiveAlarms != 1 {
		t.Fatalf("active alarms = %d, want 1", d.ActiveAlarms)
	}
	if d.Daydream.Actions != 1 || d.Daydream.ArtifactsProduced != 1 || d.Daydream.InsightsProposed != 1 {
		t.Fatalf("daydream = %+v", d.Daydream)
	}
	if d.Daydream.ChunksProcessed != 3 || d.Daydream.TokensSaved != 120 {
		t.Fatalf("daydream counters = %+v", d.Daydream)
	}
}

func TestBuildEmptyWindowIsQuiet(t *testing.T) {
	d, err := Build(context.Background(), testStore(t),
		time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if d.JobsCompleted != 0 || len(d.Failures) != 0 {
		t.Fatalf("empty window = %+v, want zeros", d)
	}
}
