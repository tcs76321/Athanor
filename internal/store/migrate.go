// Forward-only migration runner (ARCHITECTURE §23.5).
//
// Each migration executes inside its own transaction together with its
// schema_migrations bookkeeping row, so a failing migration rolls back
// completely — including kill -9 mid-migration, which SQLite recovers via
// WAL rollback journaling — leaving the prior version intact and usable.
// A rerun is always safe and becomes a no-op once all migrations are
// applied. Rollback is by restoring a backup only; there are no down
// migrations.
package store

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
)

// schemaMigrations records applied migration versions.
const schemaMigrations = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);`

// Migrate applies every pending .sql migration in fsys (files named
// NNNN_description.sql) in lexical order. Before the first pending
// migration runs, a backup of the current database is written into
// backupDir (created if needed). An empty backupDir disables backups.
func Migrate(db *sql.DB, fsys fs.FS, backupDir string) error {
	if _, err := db.Exec(schemaMigrations); err != nil {
		return fmt.Errorf("ensuring schema_migrations: %w", err)
	}
	pending, err := pendingMigrations(db, fsys)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil // rerun is a no-op
	}

	current, err := currentVersion(db)
	if err != nil {
		return err
	}
	if backupDir != "" {
		backupPath, err := Backup(db, backupDir, current)
		if err != nil {
			return fmt.Errorf("backup before migrate: %w", err)
		}
		slog.Info("pre-migrate backup written", "path", backupPath, "from_version", current)
	}

	for _, m := range pending {
		if err := applyOne(db, m); err != nil {
			// Re-read the current version for the message; fall back to the
			// pre-loop value if the read itself fails (known-issues L15).
			applied, verr := currentVersion(db)
			if verr != nil {
				applied = current
			}
			return fmt.Errorf("migration %04d_%s failed (database remains at version %d): %w",
				m.version, m.name, applied, err)
		}
		slog.Info("migration applied", "version", m.version, "name", m.name)
	}
	return nil
}

// currentVersion reports the highest applied schema version (0 if none). It
// propagates a query error (known-issues L15) so a broken database surfaces as
// a real error rather than a misleading "version 0".
func currentVersion(db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	return v, nil
}

// Version returns the schema version of the store's database. The convenience
// accessor reads 0 on a query error; callers that must distinguish a broken
// database use currentVersion through Migrate.
func (s *Store) Version() int {
	v, err := currentVersion(s.db)
	if err != nil {
		return 0
	}
	return v
}

// migration is one parsed .sql file.
type migration struct {
	version int
	name    string
	body    string
	path    string
}

// pendingMigrations lists not-yet-applied migrations in version order and
// verifies the migration set is consistent with the recorded history.
func pendingMigrations(db *sql.DB, fsys fs.FS) ([]migration, error) {
	applied := map[int]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("reading applied versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("listing embedded migrations: %w", err)
	}
	sort.Strings(entries)

	var out []migration
	seen := map[int]string{}
	for _, entry := range entries {
		m, err := readMigration(fsys, entry)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[m.version]; dup {
			return nil, fmt.Errorf("duplicate migration version %d (%q and %q)", m.version, prev, entry)
		}
		seen[m.version] = entry
		if !applied[m.version] {
			out = append(out, m)
		}
		delete(applied, m.version)
	}
	if len(applied) > 0 {
		var versions []string
		for v := range applied {
			versions = append(versions, fmt.Sprintf("%04d", v))
		}
		sort.Strings(versions)
		return nil, fmt.Errorf("schema_migrations references versions missing files: %s",
			strings.Join(versions, ", "))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// readMigration loads and names-checks one NNNN_description.sql file.
func readMigration(fsys fs.FS, entry string) (migration, error) {
	base := strings.TrimSuffix(filepath.Base(entry), ".sql")
	idx := strings.Index(base, "_")
	if idx != 4 || !allDigits(base[:4]) {
		return migration{}, fmt.Errorf("migration file %q must be named NNNN_description.sql", entry)
	}
	var version int
	if _, err := fmt.Sscanf(base[:4], "%d", &version); err != nil {
		return migration{}, fmt.Errorf("migration file %q has bad version prefix: %w", entry, err)
	}
	body, err := fs.ReadFile(fsys, entry)
	if err != nil {
		return migration{}, fmt.Errorf("reading migration %q: %w", entry, err)
	}
	return migration{
		version: version,
		name:    base[5:],
		body:    string(body),
		path:    entry,
	}, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// applyOne executes a single migration atomically with its bookkeeping row.
//
// SQLite's documented table-rebuild procedure (sqlite.org/lang_altertable,
// §7ff) requires foreign key enforcement to be OFF while a migration runs —
// PRAGMA foreign_keys is a no-op inside a transaction, so it is toggled
// around it. Safety is preserved by an explicit foreign_key_check gate
// inside the transaction: a migration that leaves referential integrity
// broken fails and rolls back like any other failing statement.
func applyOne(db *sql.DB, m migration) error {
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disabling foreign keys for migration: %w", err)
	}
	// Best-effort re-enable on every exit path; a second call after a
	// successful Commit is harmless.
	defer func() { _, _ = db.Exec(`PRAGMA foreign_keys = ON`) }()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op error, safe to ignore.
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(m.body); err != nil {
		return err
	}
	if err := checkForeignKeys(tx, m); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
		m.version, m.name,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// checkForeignKeys is the integrity gate for migrations: it fails (and
// thereby rolls back) any migration whose statements left a child row
// without its parent. Migrations may therefore rebuild parent tables
// freely; they may not commit broken references.
func checkForeignKeys(tx *sql.Tx, m migration) error {
	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("checking foreign keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var violations []string
	for rows.Next() {
		var table, parent string
		var rowid any
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("scanning foreign_key_check: %w", err)
		}
		violations = append(violations, fmt.Sprintf("%s row %v → %s", table, rowid, parent))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating foreign_key_check: %w", err)
	}
	if len(violations) > 0 {
		return fmt.Errorf("migration %04d_%s breaks referential integrity: %s",
			m.version, m.name, strings.Join(violations, "; "))
	}
	return nil
}
