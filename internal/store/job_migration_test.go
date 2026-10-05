package store

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tcs76321/athanor/migrations"
)

// migrationsExcept builds a filesystem of every embedded migration except
// those matching the given prefixes, simulating an earlier schema version
// for upgrade testing.
func migrationsExcept(t *testing.T, prefixes ...string) fstest.MapFS {
	t.Helper()
	entries, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, e := range entries {
		skip := false
		for _, p := range prefixes {
			if strings.HasPrefix(e, p) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, e)
		if err != nil {
			t.Fatal(err)
		}
		out[e] = &fstest.MapFile{Data: data}
	}
	return out
}

// seedJobWithChildren creates the parent chain plus child rows referencing
// the job — the inbound foreign keys migration 0004 must preserve across
// the jobs rebuild.
func seedJobWithChildren(t *testing.T, s *Store) {
	t.Helper()
	db := s.DB()
	steps := []struct{ name, sql string }{
		{"project", `INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','seed','code','build things that last')`},
		{"task", `INSERT INTO tasks (id, project_id, title) VALUES ('t1','p1','do the thing')`},
		{"job", `INSERT INTO jobs (id, task_id, project_id) VALUES ('j1','t1','p1')`},
		{"action", `INSERT INTO actions (id, job_id, seq, tool) VALUES ('a1','j1',1,'none')`},
		{"artifact", `INSERT INTO artifacts (id, project_id, task_id, job_id, kind) VALUES ('ar1','p1','t1','j1','code')`},
	}
	for _, step := range steps {
		if _, err := db.Exec(step.sql); err != nil {
			t.Fatalf("seeding %s: %v", step.name, err)
		}
	}
}

// TestJobStateCheckMigration proves migration 0004 (ADR-0006): the §8.1
// state CHECK lands, paused_from gains its invariant, existing jobs and
// their inbound foreign keys survive the rebuild, and index/trigger are
// restored.
func TestJobStateCheckMigration(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()

	if err := Migrate(db, migrationsExcept(t, "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020"), ""); err != nil {
		t.Fatalf("migrating to v3: %v", err)
	}
	if got := VersionOf(t, db); got != 3 {
		t.Fatalf("v3 setup: version = %d, want 3", got)
	}
	seedJobWithChildren(t, s)

	// At v3 the state column is unconstrained: junk is accepted. Real v3
	// databases never contain it (no job write path existed before M1-T4),
	// and migration 0004 deliberately fails loudly on dirty data (see
	// TestJobStateCheckMigrationRejectsDirtyState), so remove the junk row
	// before the clean-upgrade path below.
	if _, err := db.Exec(
		`INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j2','t1','p1','running')`,
	); err != nil {
		t.Fatalf("v3 setup: unconstrained state rejected: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM jobs WHERE id = 'j2'`); err != nil {
		t.Fatal(err)
	}

	// Apply 0004 (and 0005/0006, which now follow it) on top.
	if err := Migrate(db, migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("applying 0004: %v", err)
	}
	if got := VersionOf(t, db); got != 20 {
		t.Fatalf("version = %d after 0004, want 20", got)
	}

	// The pre-existing job row was preserved by the copy.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs after migration = %d (err=%v), want the seeded row preserved", n, err)
	}

	// The CHECK enforces the §8.1 set on new writes.
	if _, err := db.Exec(
		`INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j3','t1','p1','running')`,
	); err == nil {
		t.Error("unknown state 'running' accepted at v4, want CHECK rejection")
	}
	// ...including states that exist in §8.1 but are unreachable until M3/M6.
	for _, state := range []string{"evaluating", "reflecting", "awaiting_approval", "paused"} {
		if _, err := db.Exec(
			`INSERT INTO jobs (id, task_id, project_id, state) VALUES (?, 't1', 'p1', ?)`, "j-"+state, state,
		); err != nil {
			t.Errorf("§8.1 state %q rejected by schema: %v", state, err)
		}
	}

	// paused_from is only meaningful while paused, and only from
	// resumable states.
	badPausedFrom := []struct{ name, state, from string }{
		{"paused_from while running", "planning", "diverging"},
		{"paused_from queued", "paused", "queued"},
		{"paused_from terminal", "paused", "completed"},
	}
	for _, c := range badPausedFrom {
		if _, err := db.Exec(
			`INSERT INTO jobs (id, task_id, project_id, state, paused_from) VALUES (?, 't1', 'p1', ?, ?)`,
			"pf-"+c.name, c.state, c.from,
		); err == nil {
			t.Errorf("%s: paused_from (%s,%s) accepted, want rejection", c.name, c.state, c.from)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO jobs (id, task_id, project_id, state, paused_from) VALUES ('pf-ok','t1','p1','paused','planning')`,
	); err != nil {
		t.Errorf("paused job with paused_from=planning rejected: %v", err)
	}

	// Inbound foreign keys survived the rebuild: children can still be
	// inserted, and a bogus job id is still rejected.
	if _, err := db.Exec(
		`INSERT INTO actions (id, job_id, seq, tool) VALUES ('a2','j1',2,'none')`,
	); err != nil {
		t.Errorf("child insert after jobs rebuild failed (FK broken): %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO actions (id, job_id, seq, tool) VALUES ('a3','missing-job',3,'none')`,
	); err == nil {
		t.Error("FK violation against jobs not enforced after rebuild")
	}

	// Index and trigger restored on the rebuilt table.
	for _, obj := range []struct{ kind, name string }{
		{"index", "idx_jobs_task_state"},
		{"trigger", "jobs_touch_updated_at"},
	} {
		var cnt int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?`, obj.kind, obj.name,
		).Scan(&cnt); err != nil || cnt != 1 {
			t.Errorf("%s %q missing after rebuild (n=%d err=%v)", obj.kind, obj.name, cnt, err)
		}
	}

	// The rebuilt trigger still fires.
	var ts string
	if err := db.QueryRow(`SELECT updated_at FROM jobs WHERE id='j1'`).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE jobs SET attempt = 1 WHERE id='j1'`); err != nil {
		t.Fatal(err)
	}
	var ts2 string
	if err := db.QueryRow(`SELECT updated_at FROM jobs WHERE id='j1'`).Scan(&ts2); err != nil {
		t.Fatal(err)
	}
	if ts == ts2 {
		t.Error("jobs_touch_updated_at not firing on rebuilt table")
	}
}

// TestJobStateCheckMigrationRejectsDirtyState proves migration 0004 fails
// loudly — rather than silently remapping — when a pre-existing row
// carries a state outside §8.1, and that the database remains usable at
// version 3 afterwards (transactional rollback, §23.5).
func TestJobStateCheckMigrationRejectsDirtyState(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()

	if err := Migrate(db, migrationsExcept(t, "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020"), ""); err != nil {
		t.Fatalf("migrating to v3: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','seed','code','build things that last')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title) VALUES ('t1','p1','do the thing')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j1','t1','p1','running')`,
	); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db, migrations.FS, t.TempDir()); err == nil {
		t.Fatal("migration 0004 accepted a dirty state row, want loud failure")
	} else if !strings.Contains(err.Error(), "remains at version 3") {
		t.Errorf("error should state the surviving version: %v", err)
	}

	// The database is untouched and still usable at v3.
	if got := VersionOf(t, db); got != 3 {
		t.Errorf("version after failed migration = %d, want 3 (rolled back)", got)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM jobs WHERE id='j1'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "running" {
		t.Errorf("dirty row state = %q, want unchanged 'running'", state)
	}
}
