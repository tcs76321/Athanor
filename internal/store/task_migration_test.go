package store

import (
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/migrations"
)

// TestTaskLifecycleMigration proves migration 0017 (ADR-0033): tasks are
// rebuilt to the §7.3 status set with task_type, M1-era rows are remapped,
// inbound and self foreign keys survive, and the index/trigger are restored.
func TestTaskLifecycleMigration(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()

	if err := Migrate(db, migrationsExcept(t, "0017", "0018", "0019", "0020", "0021", "0022"), ""); err != nil {
		t.Fatalf("migrating to v16: %v", err)
	}
	if got := VersionOf(t, db); got != 16 {
		t.Fatalf("v16 setup: version = %d, want 16", got)
	}

	seed := []string{
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','seed','code','build things that last')`,
		`INSERT INTO tasks (id, project_id, title, status) VALUES ('t-parent','p1','phase','pending')`,
		`INSERT INTO tasks (id, project_id, title, status, allowed_tools_json) VALUES ('t-run','p1','running task','in_progress','["execute_code"]')`,
		`INSERT INTO tasks (id, project_id, title, status) VALUES ('t-done','p1','done task','done')`,
		`INSERT INTO tasks (id, project_id, parent_task_id, title, status) VALUES ('t-child','p1','t-parent','child','pending')`,
		`INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j1','t-run','p1','completed')`,
		`INSERT INTO artifacts (id, project_id, task_id, kind) VALUES ('ar1','p1','t-run','code')`,
	}
	for _, q := range seed {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seeding %q: %v", q, err)
		}
	}

	if err := Migrate(db, migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("applying 0017: %v", err)
	}
	if got := VersionOf(t, db); got != 22 {
		t.Fatalf("version = %d after 0017, want 22", got)
	}

	// Rows preserved and M1 statuses remapped.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("tasks after migration = %d (err=%v), want 4 preserved", n, err)
	}
	wantStatus := map[string]string{
		"t-parent": "pending",
		"t-run":    "running",
		"t-done":   "completed",
		"t-child":  "pending",
	}
	for id, want := range wantStatus {
		var got, taskType, tools string
		if err := db.QueryRow(`SELECT status, task_type, allowed_tools_json FROM tasks WHERE id=?`, id).
			Scan(&got, &taskType, &tools); err != nil {
			t.Fatalf("reading %s: %v", id, err)
		}
		if got != want {
			t.Errorf("%s status = %q, want %q", id, got, want)
		}
		if taskType != "one_time" {
			t.Errorf("%s task_type = %q, want one_time", id, taskType)
		}
	}
	var tools string
	if err := db.QueryRow(`SELECT allowed_tools_json FROM tasks WHERE id='t-run'`).Scan(&tools); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tools, "execute_code") {
		t.Errorf("allowed_tools lost in rebuild: %q", tools)
	}
	var parent string
	if err := db.QueryRow(`SELECT COALESCE(parent_task_id,'') FROM tasks WHERE id='t-child'`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent != "t-parent" {
		t.Errorf("child parent = %q, want t-parent", parent)
	}

	// The canonical CHECK is enforced, and the M1 values are gone.
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, status) VALUES ('bad','p1','x','in_progress')`); err == nil {
		t.Error("M1 status 'in_progress' accepted at v17, want rejection")
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, status) VALUES ('ok','p1','x','running')`); err != nil {
		t.Errorf("canonical status 'running' rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, task_type) VALUES ('bt','p1','x','weird')`); err == nil {
		t.Error("unknown task_type accepted, want rejection")
	}

	// Inbound and self foreign keys survive.
	if _, err := db.Exec(`INSERT INTO jobs (id, task_id, project_id) VALUES ('j2','ok','p1')`); err != nil {
		t.Errorf("child job insert after tasks rebuild failed (FK broken): %v", err)
	}
	if _, err := db.Exec(`INSERT INTO jobs (id, task_id, project_id) VALUES ('j3','missing','p1')`); err == nil {
		t.Error("FK violation against tasks not enforced after rebuild")
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, parent_task_id) VALUES ('orphan','p1','x','missing')`); err == nil {
		t.Error("self FK violation not enforced after rebuild")
	}

	// Indexes and trigger restored; the trigger still fires.
	for _, name := range []string{"idx_tasks_project_status", "idx_tasks_goal", "idx_tasks_parent"} {
		var cnt int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&cnt); err != nil || cnt != 1 {
			t.Errorf("index %q missing after rebuild (n=%d err=%v)", name, cnt, err)
		}
	}
	var ts1 string
	if err := db.QueryRow(`SELECT updated_at FROM tasks WHERE id='ok'`).Scan(&ts1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := db.Exec(`UPDATE tasks SET priority=1 WHERE id='ok'`); err != nil {
		t.Fatal(err)
	}
	var ts2 string
	if err := db.QueryRow(`SELECT updated_at FROM tasks WHERE id='ok'`).Scan(&ts2); err != nil {
		t.Fatal(err)
	}
	if ts1 == ts2 {
		t.Error("tasks_touch_updated_at not firing on rebuilt table")
	}
}
