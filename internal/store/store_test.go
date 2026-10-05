package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/migrations"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpenAppliesPragmas(t *testing.T) {
	s, _ := openTemp(t)
	for name, want := range map[string]string{
		"PRAGMA journal_mode": "wal",
		"PRAGMA foreign_keys": "1",
		"PRAGMA busy_timeout": "5000",
		"PRAGMA synchronous":  "1", // NORMAL
	} {
		var got string
		if err := s.DB().QueryRow(name).Scan(&got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestMigrateAppliesEmbeddedSchema(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()

	if err := Migrate(db, migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := s.Version(); got != 14 {
		t.Fatalf("version = %d, want 14", got)
	}

	wantTables := []string{
		"projects", "goals", "tasks", "jobs", "actions", "artifacts", "events",
		"corrections", "hitl_requests", "prompt_templates", "personas", "system_state",
		"context_chunks", "dormant_index", "context_active", "compacted_memory",
		"compacted_memory_fts", "dormant_index_fts", "memory_embeddings",
		"indexed_sources",
	}
	for _, tbl := range wantTables {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %q missing (n=%d err=%v)", tbl, n, err)
		}
	}

	// FK constraints are enforced end-to-end.
	if _, err := db.Exec(
		`INSERT INTO goals (id, project_id, text) VALUES ('g1','nope','this goal text is long enough')`,
	); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("FK violation not rejected: %v", err)
	}

	// updated_at trigger fires on update.
	var triggers int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='projects_touch_updated_at'`,
	).Scan(&triggers); err != nil || triggers != 1 {
		t.Fatalf("projects_touch_updated_at trigger missing (err=%v)", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','code','build things')`,
	); err != nil {
		t.Fatal(err)
	}
	readUpdated := func() string {
		var ts string
		if err := db.QueryRow(`SELECT updated_at FROM projects WHERE id='p1'`).Scan(&ts); err != nil {
			t.Fatalf("reading updated_at: %v", err)
		}
		return ts
	}
	first := readUpdated()
	time.Sleep(5 * time.Millisecond)
	if _, err := db.Exec(`UPDATE projects SET status='paused' WHERE id='p1'`); err != nil {
		t.Fatal(err)
	}
	second := readUpdated()
	if first == "" || second == "" || first == second {
		t.Errorf("updated_at not maintained by trigger: %q -> %q", first, second)
	}
}

func TestMigrateRerunIsNoop(t *testing.T) {
	s, _ := openTemp(t)
	dir := t.TempDir()
	if err := Migrate(s.DB(), migrations.FS, dir); err != nil {
		t.Fatal(err)
	}
	var countBefore int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(s.DB(), migrations.FS, dir); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	var countAfter int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&countAfter); err != nil {
		t.Fatal(err)
	}
	if countAfter != countBefore {
		t.Errorf("schema_migrations rows changed on rerun: %d -> %d", countBefore, countAfter)
	}
}

func TestMigrateBackupCreated(t *testing.T) {
	s, _ := openTemp(t)
	backupDir := t.TempDir()
	if err := Migrate(s.DB(), migrations.FS, backupDir); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(backupDir, "athanor-v0000-*.db"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one pre-migrate backup, got %v", matches)
	}
	bk, err := Open(matches[0])
	if err != nil {
		t.Fatalf("backup is not openable as SQLite: %v", err)
	}
	_ = bk.Close()
}
