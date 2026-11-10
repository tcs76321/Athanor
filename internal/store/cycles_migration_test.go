package store

import (
	"testing"

	"github.com/tcs76321/athanor/migrations"
)

// TestCyclesMigrationBackfillsLegacyCycle proves migration 0048: an existing
// project gets one legacy cycle-0 row and every existing task is attached to
// it. The backfill is what keeps old one-shot projects valid after the cycle
// model lands.
func TestCyclesMigrationBackfillsLegacyCycle(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()
	if err := Migrate(db, migrationsExcept(t, "0048", "0049"), ""); err != nil {
		t.Fatalf("migrate to 47: %v", err)
	}
	if got := VersionOf(t, db); got != 47 {
		t.Fatalf("version = %d, want 47", got)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','legacy','code','Build a thing.')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title) VALUES ('t1','p1','Build it')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db, migrations.FS, ""); err != nil {
		t.Fatalf("full migrate: %v", err)
	}
	var id, kind, state, goal string
	if err := db.QueryRow(`SELECT id, kind, state, goal FROM project_cycles WHERE project_id = 'p1'`).
		Scan(&id, &kind, &state, &goal); err != nil {
		t.Fatal(err)
	}
	if id != "cycle-p1" || kind != "legacy" || state != "legacy" || goal != "Build a thing." {
		t.Errorf("legacy cycle = %q/%q/%q/%q", id, kind, state, goal)
	}
	var cycleID string
	if err := db.QueryRow(`SELECT cycle_id FROM tasks WHERE id = 't1'`).Scan(&cycleID); err != nil {
		t.Fatal(err)
	}
	if cycleID != "cycle-p1" {
		t.Errorf("task cycle = %q, want cycle-p1", cycleID)
	}
}
