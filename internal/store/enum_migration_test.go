package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/migrations"
)

// seedV2Rows creates project/task/job parents plus rows carrying pre-0003
// enum values in corrections and hitl_requests.
func seedV2Rows(t *testing.T, s *Store) {
	t.Helper()
	db := s.DB()
	steps := []struct {
		name string
		sql  string
	}{
		{"project", `INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','seed','code','build things that last')`},
		{"task", `INSERT INTO tasks (id, project_id, title) VALUES ('t1','p1','do the thing')`},
		{"job", `INSERT INTO jobs (id, task_id, project_id) VALUES ('j1','t1','p1')`},
		{"correction minor", `INSERT INTO corrections (id, project_id, category, severity, derived_rule) VALUES ('c1','p1','style','minor','prefer small functions')`},
		{"correction major", `INSERT INTO corrections (id, project_id, category, severity, derived_rule) VALUES ('c2','p1','security','major','never log secrets')`},
		{"correction critical", `INSERT INTO corrections (id, source_job_id, category, severity, derived_rule) VALUES ('c3','j1','architecture','critical','no global state')`},
		{"hitl info", `INSERT INTO hitl_requests (id, job_id, type, severity) VALUES ('h1','j1','approval','info')`},
		{"hitl normal", `INSERT INTO hitl_requests (id, job_id, type, severity) VALUES ('h2','j1','approval','normal')`},
		{"artifact code", `INSERT INTO artifacts (id, project_id, task_id, kind) VALUES ('a1','p1','t1','code')`},
	}
	for _, step := range steps {
		if _, err := db.Exec(step.sql); err != nil {
			t.Fatalf("seeding %s: %v", step.name, err)
		}
	}
}

func TestCanonicalEnumMigration(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()

	// Build v2 state and prove the old constraints reject canonical values.
	if err := Migrate(db, migrationsExcept(t, "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021", "0022", "0023", "0024"), ""); err != nil {
		t.Fatalf("migrating to v2: %v", err)
	}
	if got := VersionOf(t, db); got != 2 {
		t.Fatalf("v2 setup: version = %d, want 2", got)
	}
	seedV2Rows(t, s)

	pre := []struct{ name, sql string }{
		{"corrections severity 'low'", `INSERT INTO corrections (id, category, severity, derived_rule) VALUES ('cx','style','low','x')`},
		{"hitl severity 'medium'", `INSERT INTO hitl_requests (id, type, severity) VALUES ('hx','approval','medium')`},
		{"artifacts kind 'media'", `INSERT INTO artifacts (id, project_id, kind) VALUES ('ax','p1','media')`},
	}
	for _, c := range pre {
		if _, err := db.Exec(c.sql); err == nil {
			t.Errorf("at v2, %s was unexpectedly accepted", c.name)
		}
	}

	// Apply 0003 (and 0004/0005/0006, which now follow it) on top.
	if err := Migrate(db, migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("applying 0003: %v", err)
	}
	if got := VersionOf(t, db); got != 24 {
		t.Fatalf("version = %d after full migrate, want 24", got)
	}

	// Severity remapping is correct.
	wantSeverity := map[string]string{
		"c1": "low",      // minor → low
		"c2": "medium",   // major → medium
		"c3": "critical", // unchanged
		"h1": "low",      // info → low
		"h2": "medium",   // normal → medium
	}
	for id, want := range wantSeverity {
		table := "corrections"
		if strings.HasPrefix(id, "h") {
			table = "hitl_requests"
		}
		var got string
		if err := db.QueryRow(`SELECT severity FROM `+table+` WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatalf("%s.%s survived migration: %v", table, id, err)
		}
		if got != want {
			t.Errorf("%s.%s severity = %q, want %q", table, id, got, want)
		}
	}

	// Old artifact rows carried over; canonical kinds now accepted.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artifacts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("artifacts count = %d err=%v, want 1", n, err)
	}
	for _, kind := range []string{"media", "configuration", "proposal"} {
		if _, err := db.Exec(
			`INSERT INTO artifacts (id, project_id, kind) VALUES (?, 'p1', ?)`, "kind-"+kind, kind,
		); err != nil {
			t.Errorf("canonical artifacts.kind %q rejected: %v", kind, err)
		}
	}

	// New CHECK constraints enforce the canonical sets.
	post := []struct{ name, sql string }{
		{"corrections severity 'minor'", `INSERT INTO corrections (id, category, severity, derived_rule) VALUES ('cy','style','minor','y')`},
		{"hitl severity 'info'", `INSERT INTO hitl_requests (id, type, severity) VALUES ('hy','approval','info')`},
		{"artifacts kind 'unknown'", `INSERT INTO artifacts (id, project_id, kind) VALUES ('ay','p1','unknown')`},
	}
	for _, c := range post {
		if _, err := db.Exec(c.sql); err == nil {
			t.Errorf("at v3, %s was unexpectedly accepted", c.name)
		}
	}

	// Indexes and trigger restored on rebuilt tables.
	for _, obj := range []struct{ kind, name string }{
		{"index", "idx_artifacts_project"},
		{"index", "idx_corrections_project_status"},
		{"index", "idx_hitl_status"},
		{"trigger", "artifacts_touch_updated_at"},
	} {
		var cnt int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?`, obj.kind, obj.name,
		).Scan(&cnt); err != nil || cnt != 1 {
			t.Errorf("%s %q missing after rebuild (n=%d err=%v)", obj.kind, obj.name, cnt, err)
		}
	}

	// The rebuilt artifacts trigger still fires.
	readUpdated := func() string {
		var ts string
		if err := db.QueryRow(`SELECT updated_at FROM artifacts WHERE id='a1'`).Scan(&ts); err != nil {
			t.Fatalf("reading updated_at: %v", err)
		}
		return ts
	}
	first := readUpdated()
	time.Sleep(5 * time.Millisecond)
	if _, err := db.Exec(`UPDATE artifacts SET storage_path='/x' WHERE id='a1'`); err != nil {
		t.Fatal(err)
	}
	if second := readUpdated(); second == first {
		t.Error("artifacts_touch_updated_at not firing on rebuilt table")
	}

	// Event log still append-only and working end-to-end.
	ctx := context.Background()
	id1, err := s.AppendEvent(ctx, Event{Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.AppendEvent(ctx, Event{Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 >= id2 {
		t.Errorf("event ids not increasing: %d then %d", id1, id2)
	}
}
