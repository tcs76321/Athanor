package main

import (
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// TestEvidenceRows pins the F5 reducer: a terminal job with a strategy outcome
// becomes the same jobMetrics row shape the soak report consumes, with the
// model label derived from llm_call events.
func TestEvidenceRows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "athanor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`
		INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','code','do work');
		INSERT INTO tasks (id, project_id, title) VALUES ('t1','p1','fix the parser');
		INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j1','t1','p1','completed');
		INSERT INTO strategy_profiles (id, job_id, project_id, archetype) VALUES ('sp1','j1','p1','code');
		INSERT INTO strategy_outcomes (id, job_id, strategy_profile_id, result, score, evaluator_confidence)
			VALUES ('o1','j1','sp1','accepted_new',0.9,0.8);
		INSERT INTO events (job_id, category, data_json) VALUES
			('j1','jobs','{"event":"llm_call","model":"m1"}'),
			('j1','jobs','{"event":"llm_call","model":"m1"}'),
			('j1','jobs','{"event":"llm_call","model":"m2"}');
	`); err != nil {
		t.Fatal(err)
	}

	rows, err := evidenceRows(st.DB(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	m := rows[0]
	if m.GoalName != "fix the parser" || m.Archetype != "code" || m.Tier != "IRL" {
		t.Errorf("row = %+v", m)
	}
	if m.ModelLabel != "m1" || m.Model != "m1" {
		t.Errorf("model = %q/%q, want the most frequent m1", m.ModelLabel, m.Model)
	}
	if m.Winner != "new" || m.State != "completed" || m.Score != 0.9 {
		t.Errorf("outcome = %+v, want accepted_new/0.9", m)
	}
}
