package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func newTestStore(t *testing.T) *store.Store {
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

func countBackupEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	evs, err := st.QueryEvents(context.Background(), store.EventFilter{Category: "backup"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if strings.Contains(e.DataJSON, "backup_created") {
			n++
		}
	}
	return n
}

func TestSnapshotPruneRestore(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "athanor.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.AppendEvent(ctx, store.Event{Category: "jobs", Data: map[string]any{"event": "before"}}); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(t.TempDir(), "backups")
	path, err := Snapshot(st.DB(), backupDir, st.Version())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// A change after the snapshot must not survive the restore.
	if _, err := st.AppendEvent(ctx, store.Event{Category: "jobs", Data: map[string]any{"event": "after"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Restore(path, dbPath); err != nil {
		t.Fatalf("restore: %v", err)
	}

	restored, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen restored: %v", err)
	}
	defer func() { _ = restored.Close() }()
	evs, err := restored.QueryEvents(ctx, store.EventFilter{Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	var sawBefore, sawAfter bool
	for _, e := range evs {
		if strings.Contains(e.DataJSON, "before") {
			sawBefore = true
		}
		if strings.Contains(e.DataJSON, "after") {
			sawAfter = true
		}
	}
	if !sawBefore {
		t.Error("restored database is missing pre-snapshot state")
	}
	if sawAfter {
		t.Error("restored database retains post-snapshot state (restore failed)")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"athanor-v0001-20260101T000001Z.db",
		"athanor-v0001-20260101T000002Z.db",
		"athanor-v0001-20260101T000003Z.db",
		"athanor-v0001-20260101T000004Z.db",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-backup.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := Prune(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %d, want 2", len(removed))
	}
	// The two newest remain, and the non-backup file is untouched.
	for _, want := range []string{"athanor-v0001-20260101T000003Z.db", "athanor-v0001-20260101T000004Z.db", "not-a-backup.txt"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("expected %s to remain: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "athanor-v0001-20260101T000001Z.db")); err == nil {
		t.Error("oldest snapshot was not pruned")
	}
}

func TestPruneNonPositiveKeepIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "athanor-v0001-20260101T000001Z.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := Prune(dir, 0)
	if err != nil || len(removed) != 0 {
		t.Fatalf("Prune(dir, 0) = %v, %v; want no-op", removed, err)
	}
}
