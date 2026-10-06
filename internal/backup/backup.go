package backup

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/store"
)

// Snapshot writes a consistent DB snapshot into dir (VACUUM INTO; §23.4).
func Snapshot(db *sql.DB, dir string, version int) (string, error) {
	return store.Backup(db, dir, version)
}

// Prune keeps the newest `keep` snapshots in dir and removes the rest. It
// returns the removed paths. A non-positive keep is a no-op.
func Prune(dir string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: listing %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "athanor-v") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return nil, nil
	}
	// Names embed a UTC timestamp (athanor-vNNNN-YYYYMMDDTHHMMSSZ.db), so a
	// lexical sort is chronological.
	sort.Strings(names)
	var removed []string
	for _, name := range names[:len(names)-keep] {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("backup: removing %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	return removed, nil
}

// Restore replaces dbPath with the snapshot at backupPath, atomically
// (temp + rename). The daemon must be stopped: it holds the database open.
func Restore(backupPath, dbPath string) error {
	src, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("backup: opening %s: %w", backupPath, err)
	}
	defer func() { _ = src.Close() }()

	dir := filepath.Dir(dbPath)
	tmp, err := os.CreateTemp(dir, ".restore-*.db")
	if err != nil {
		return fmt.Errorf("backup: creating temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("backup: copying snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("backup: syncing snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("backup: closing snapshot: %w", err)
	}
	// Drop stale WAL/SHM alongside the replaced database so SQLite cannot
	// replay a pre-restore journal over the restored bytes.
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Rename(tmpName, dbPath); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("backup: replacing %s: %w", dbPath, err)
	}
	return nil
}
