// Package store provides the SQLite persistence substrate for Athanor
// (ARCHITECTURE §23): WAL pragmas, a forward-only embedded migration
// runner with backup-before-migrate (and a foreign-key integrity gate),
// and (from M0-T6) the append-only EventLog API.
//
// ADR-0125 shapes the pool: one writer connection (writes are serialized by
// SQLite anyway) plus a read-only pool so UI/SSE/digest reads do not block a
// job's short write transaction. The writer uses `_txlock=immediate`, which
// takes the write lock at BEGIN and avoids a read-to-write upgrade deadlock.
package store

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Store wraps the daemon's SQLite database.
type Store struct {
	write *sql.DB
	read  *sql.DB
	path  string
}

// Option configures Open.
type Option func(*openOptions)

type openOptions struct {
	readConns int
}

// WithReadConns sets the read-only pool size. n < 1 keeps the default (4).
func WithReadConns(n int) Option {
	return func(o *openOptions) {
		if n > 0 {
			o.readConns = n
		}
	}
}

// escapeDSNPath percent-encodes the characters that terminate or confuse a
// `file:` SQLite DSN (known-issues L16), leaving path separators intact. `%`
// is encoded first so an existing escape is not double-decoded.
func escapeDSNPath(p string) string {
	return strings.NewReplacer(
		"%", "%25", "?", "%3F", "#", "%23", " ", "%20",
	).Replace(p)
}

// Open opens (creating if needed) the database at path and applies the
// pragmas mandated by ARCHITECTURE §23.1. Writes use one connection; reads
// use a separate read-only pool (ADR-0125).
func Open(path string, opts ...Option) (*Store, error) {
	o := openOptions{readConns: 4}
	for _, opt := range opts {
		opt(&o)
	}
	writeDSN := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_foreign_keys=ON&_txlock=immediate",
		escapeDSNPath(path),
	)
	write, err := sql.Open("sqlite3", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	if err := write.Ping(); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("pinging %s: %w", path, err)
	}

	// The writer has created the file and set WAL. Open the read pool after
	// that so a fresh database is readable and the WAL sidecars exist.
	readDSN := fmt.Sprintf(
		"file:%s?mode=ro&_busy_timeout=5000&_foreign_keys=ON",
		escapeDSNPath(path),
	)
	read, err := sql.Open("sqlite3", readDSN)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("opening read pool for %s: %w", path, err)
	}
	read.SetMaxOpenConns(o.readConns)
	read.SetMaxIdleConns(o.readConns)
	if err := read.Ping(); err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, fmt.Errorf("pinging read pool for %s: %w", path, err)
	}
	return &Store{write: write, read: read, path: path}, nil
}

// DB exposes the writer handle for packages building on the store. Writes must
// go through this handle; reads should use ReadDB when possible.
func (s *Store) DB() *sql.DB { return s.write }

// ReadDB exposes the read-only pool. It is safe for concurrent reads and does
// not block on a writer transaction. It falls back to the writer only for a
// Store constructed without Open (tests that build a bare value); production
// always has a read pool.
func (s *Store) ReadDB() *sql.DB {
	if s.read != nil {
		return s.read
	}
	return s.write
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes both pools.
func (s *Store) Close() error {
	var first error
	if s.read != nil {
		if err := s.read.Close(); err != nil {
			first = err
		}
	}
	if s.write != nil {
		if err := s.write.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
