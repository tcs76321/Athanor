package retention

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func setup(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "athanor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return st
}

func insertEvent(t *testing.T, st *store.Store, ts string) int64 {
	t.Helper()
	res, err := st.DB().Exec(
		`INSERT INTO events (ts, category, level, data_json) VALUES (?, 'jobs', 'info', '{"event":"x"}')`, ts)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func countEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.ReadDB().QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestArchiveEventsMovesOldRows(t *testing.T) {
	st := setup(t)
	now := time.Now().UTC()
	old := now.AddDate(-2, 0, 0).Format("2006-01-02T15:04:05.000Z")
	for i := 0; i < 3; i++ {
		insertEvent(t, st, old)
	}
	newer := now.Format("2006-01-02T15:04:05.000Z")
	insertEvent(t, st, newer)
	insertEvent(t, st, newer)

	dir := t.TempDir()
	res, err := ArchiveEvents(context.Background(), st, now.AddDate(-1, 0, 0), Options{ArchiveDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived != 3 || res.Files != 1 {
		t.Fatalf("result = %+v, want 3 events in 1 file", res)
	}
	if got := countEvents(t, st); got != 2 {
		t.Errorf("events remaining = %d, want 2", got)
	}
	archives, err := ListArchives(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 {
		t.Fatalf("archives = %d, want 1", len(archives))
	}
	a := archives[0]
	path := filepath.Join(dir, a.Path)
	digest, size, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if digest != a.SHA256 || size != a.Bytes {
		t.Errorf("manifest digest/size = %s/%d, file = %s/%d", a.SHA256, a.Bytes, digest, size)
	}
	// The archive is valid gzip JSONL with the original rows.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	dec := json.NewDecoder(gz)
	n := 0
	for dec.More() {
		var row eventRow
		if err := dec.Decode(&row); err != nil {
			t.Fatal(err)
		}
		if row.Category != "jobs" || string(row.Data) != `{"event":"x"}` {
			t.Errorf("row = %+v", row)
		}
		n++
	}
	if n != 3 {
		t.Errorf("archive rows = %d, want 3", n)
	}
}

func TestArchiveEventsDryRunChangesNothing(t *testing.T) {
	st := setup(t)
	old := time.Now().UTC().AddDate(-2, 0, 0).Format("2006-01-02T15:04:05.000Z")
	insertEvent(t, st, old)
	dir := t.TempDir()
	res, err := ArchiveEvents(context.Background(), st, time.Now().UTC().AddDate(-1, 0, 0),
		Options{ArchiveDir: dir, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived != 1 || !res.DryRun {
		t.Fatalf("result = %+v, want 1 dry-run event", res)
	}
	if got := countEvents(t, st); got != 1 {
		t.Errorf("events = %d, want 1 (dry run must not delete)", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("archive dir entries = %d, want 0", len(entries))
	}
}

func TestArchiveEventsIsIdempotent(t *testing.T) {
	st := setup(t)
	old := time.Now().UTC().AddDate(-2, 0, 0).Format("2006-01-02T15:04:05.000Z")
	insertEvent(t, st, old)
	cutoff := time.Now().UTC().AddDate(-1, 0, 0)
	dir := t.TempDir()
	if _, err := ArchiveEvents(context.Background(), st, cutoff, Options{ArchiveDir: dir}); err != nil {
		t.Fatal(err)
	}
	res, err := ArchiveEvents(context.Background(), st, cutoff, Options{ArchiveDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived != 0 {
		t.Errorf("second pass archived %d, want 0", res.Archived)
	}
}

func TestArchiveEventsBatches(t *testing.T) {
	st := setup(t)
	old := time.Now().UTC().AddDate(-2, 0, 0).Format("2006-01-02T15:04:05.000Z")
	for i := 0; i < 5; i++ {
		insertEvent(t, st, old)
	}
	res, err := ArchiveEvents(context.Background(), st, time.Now().UTC().AddDate(-1, 0, 0),
		Options{ArchiveDir: t.TempDir(), Batch: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived != 5 || res.Files != 3 {
		t.Errorf("result = %+v, want 5 events in 3 batches", res)
	}
}

func TestGuardStillBlocksNormalDelete(t *testing.T) {
	st := setup(t)
	insertEvent(t, st, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	_, err := st.DB().Exec(`DELETE FROM events`)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("normal delete err = %v, want append-only rejection", err)
	}
	var active int
	if err := st.ReadDB().QueryRow(`SELECT active FROM retention_guard WHERE id = 1`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Errorf("guard active = %d, want 0 after a normal delete", active)
	}
}
