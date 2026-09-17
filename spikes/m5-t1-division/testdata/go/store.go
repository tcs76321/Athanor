// Package store provides the SQLite persistence substrate for Athanor
// (ARCHITECTURE §23): WAL pragmas, a forward-only embedded migration
// runner with backup-before-migrate (and a foreign-key integrity gate),
// and (from M0-T6) the append-only EventLog API.
//
// Driver choice follows the task-000 spike findings
// (docs/sqlite-setup.md): mattn/go-sqlite3 with CGO, single connection,
// so sqlite-vec extension loading has stable connection affinity later.
package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Store wraps the daemon's single SQLite database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database at path and applies the
// pragmas mandated by ARCHITECTURE §23.1. The pool is capped at one
// connection per the spike findings.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_foreign_keys=ON",
		path,
	)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging %s: %w", path, err)
	}
	return &Store{db: db, path: path}, nil
}

// DB exposes the underlying handle for packages building on the store.
func (s *Store) DB() *sql.DB { return s.db }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
