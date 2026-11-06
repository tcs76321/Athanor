package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tcs76321/athanor/migrations"
)

// openTemp is defined in store_test.go.

// TestFailedMigrationLeavesPriorVersionIntact proves the interrupted-
// migration guarantee: a migration failing mid-way rolls back entirely,
// the database stays at its prior version and remains fully usable, and a
// subsequent rerun neither corrupts nor double-applies anything. A kill -9
// mid-transaction exercises the same rollback path via WAL recovery.
func TestFailedMigrationLeavesPriorVersionIntact(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()

	good := fstest.MapFS{
		"0001_create_thing.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE things (id TEXT PRIMARY KEY, val TEXT NOT NULL);"),
		},
	}
	if err := Migrate(db, good, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO things VALUES ('t1','hello')`); err != nil {
		t.Fatal(err)
	}

	broken := fstest.MapFS{
		"0001_create_thing.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE things (id TEXT PRIMARY KEY, val TEXT NOT NULL);"),
		},
		"0002_add_stuff.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE stuff (id TEXT);\nINSERT INTO nonexistent_table VALUES (1);"),
		},
	}
	err := Migrate(db, broken, "")
	if err == nil {
		t.Fatal("broken migration succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "remains at version 1") {
		t.Errorf("error should report surviving version: %v", err)
	}

	if got := VersionOf(t, db); got != 1 {
		t.Fatalf("version = %d after failed migration, want 1", got)
	}
	var val string
	if err := db.QueryRow(`SELECT val FROM things WHERE id='t1'`).Scan(&val); err != nil || val != "hello" {
		t.Fatalf("pre-existing data lost or wrong: val=%q err=%v", val, err)
	}
	var stuff int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stuff'`,
	).Scan(&stuff); err != nil {
		t.Fatalf("checking for leaked 'stuff' table: %v", err)
	}
	if stuff != 0 {
		t.Fatalf("partial migration leaked table 'stuff' (count=%d)", stuff)
	}

	// Rerunning the same failing set fails again but changes nothing.
	if err := Migrate(db, broken, ""); err == nil {
		t.Fatal("second run of broken set succeeded")
	}
	if got := VersionOf(t, db); got != 1 {
		t.Fatalf("version = %d after failed rerun, want 1", got)
	}

	// Rerunning only applied migrations is a no-op success.
	if err := Migrate(db, good, ""); err != nil {
		t.Fatalf("no-op rerun failed: %v", err)
	}
}

func TestModifiedAppliedMigrationRejected(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()
	original := fstest.MapFS{
		"0001_create_thing.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE things (id TEXT PRIMARY KEY);"),
		},
	}
	if err := Migrate(db, original, ""); err != nil {
		t.Fatal(err)
	}
	modified := fstest.MapFS{
		"0001_create_thing.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE things (id TEXT PRIMARY KEY, extra TEXT);"),
		},
	}
	err := Migrate(db, modified, "")
	if err == nil || !strings.Contains(err.Error(), "was modified after it was applied") {
		t.Fatalf("modified applied migration err = %v, want a checksum rejection", err)
	}
}

func TestDuplicateMigrationVersionsRejected(t *testing.T) {
	s, _ := openTemp(t)
	fs := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE a (id TEXT);")},
		"0001_b.sql": &fstest.MapFile{Data: []byte("CREATE TABLE b (id TEXT);")},
	}
	if err := Migrate(s.DB(), fs, ""); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want duplicate version error", err)
	}
}

// TestForeignKeyIntegrityGate proves the runner's migration-time safety
// net: foreign key enforcement is relaxed while a migration runs (so
// parent tables can be rebuilt per SQLite's documented procedure), but a
// migration that ends with broken references fails, rolls back, and
// leaves FK enforcement re-enabled for normal operation.
func TestForeignKeyIntegrityGate(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()

	good := fstest.MapFS{
		"0001_parents.sql": &fstest.MapFile{Data: []byte(
			"CREATE TABLE parents (id TEXT PRIMARY KEY);\n" +
				"CREATE TABLE children (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parents(id));\n" +
				"INSERT INTO parents (id) VALUES ('p1');\n" +
				"INSERT INTO children (id, parent_id) VALUES ('c1','p1');")},
	}
	if err := Migrate(db, good, ""); err != nil {
		t.Fatal(err)
	}

	// This migration drops the parent while a child row still references
	// it — referential integrity is broken, so it must fail and roll back.
	broken := fstest.MapFS{
		"0001_parents.sql": good["0001_parents.sql"],
		"0002_orphan.sql":  &fstest.MapFile{Data: []byte("DROP TABLE parents;")},
	}
	err := Migrate(db, broken, "")
	if err == nil || !strings.Contains(err.Error(), "referential integrity") {
		t.Fatalf("err = %v, want referential integrity failure", err)
	}
	if got := VersionOf(t, db); got != 1 {
		t.Fatalf("version = %d after rejected migration, want 1", got)
	}

	// FK enforcement is back on for normal writes.
	if _, err := db.Exec(
		`INSERT INTO children (id, parent_id) VALUES ('c2','missing')`,
	); err == nil {
		t.Error("FK enforcement not re-enabled after failed migration")
	}

	// A migration that rebuilds the parent correctly still succeeds.
	rebuild := fstest.MapFS{
		"0001_parents.sql": good["0001_parents.sql"],
		"0003_rebuild.sql": &fstest.MapFile{Data: []byte(
			"CREATE TABLE parents_new (id TEXT PRIMARY KEY);\n" +
				"INSERT INTO parents_new SELECT id FROM parents;\n" +
				"DROP TABLE parents;\n" +
				"ALTER TABLE parents_new RENAME TO parents;")},
	}
	if err := Migrate(db, rebuild, ""); err != nil {
		t.Fatalf("legitimate parent rebuild rejected: %v", err)
	}
	if got := VersionOf(t, db); got != 3 {
		t.Fatalf("version = %d after rebuild, want 3", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM children`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("children survived = %d (err=%v), want 1", n, err)
	}
}

func TestBadMigrationNamesRejected(t *testing.T) {
	s, _ := openTemp(t)
	fs := fstest.MapFS{
		"create_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE a (id TEXT);")},
	}
	if err := Migrate(s.DB(), fs, ""); err == nil || !strings.Contains(err.Error(), "NNNN_description.sql") {
		t.Fatalf("err = %v, want naming error", err)
	}
}

func TestEventsTableIsAppendOnly(t *testing.T) {
	s, _ := openTemp(t)
	if err := Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	db := s.DB()
	if _, err := db.Exec(
		`INSERT INTO events (category, data_json) VALUES ('jobs', '{"x":1}')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET category='hacked'`); err == nil {
		t.Error("UPDATE on events accepted")
	}
	if _, err := db.Exec(`DELETE FROM events`); err == nil {
		t.Error("DELETE on events accepted")
	}
}

// TestNoEventsMutationInSource guards the invariant that this package's
// SQL never mutates the append-only events table. Only production sources
// are scanned (test fixtures legitimately contain such strings).
func TestNoEventsMutationInSource(t *testing.T) {
	files, err := filepath.Glob("./*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatal(readErr)
		}
		src := strings.ToUpper(string(data))
		if strings.Contains(src, "UPDATE"+" EVENTS") || strings.Contains(src, "DELETE FROM"+" EVENTS") {
			t.Errorf("%s contains an events mutation statement", f)
		}
	}
}

func VersionOf(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
