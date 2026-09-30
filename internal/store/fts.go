package store

import (
	"context"
	"database/sql"
	"fmt"
)

// CheckFTS5 verifies that the SQLite build has FTS5 compiled in
// (ARCHITECTURE §23; ADR-0026 §1).
//
// FTS5 is not part of the default mattn/go-sqlite3 amalgamation: it is
// enabled by the `sqlite_fts5` build tag (docs/sqlite-setup.md). The MCE
// retrieval index added in migration 0013 is an FTS5 virtual table, so a
// tag-less binary would fail with a cryptic "no such module: fts5" the
// first time a migration or query touched it. This preflight runs before
// Migrate and turns that into a named, actionable error.
//
// The probe is sqlite_compileoption_used('ENABLE_FTS5'), which returns 1
// when the amalgamation was built with FTS5 and 0 otherwise.
func CheckFTS5(ctx context.Context, db *sql.DB) error {
	var enabled int
	if err := db.QueryRowContext(ctx,
		`SELECT sqlite_compileoption_used('ENABLE_FTS5')`).Scan(&enabled); err != nil {
		return fmt.Errorf("store: probing FTS5 support: %w", err)
	}
	if enabled == 0 {
		return fmt.Errorf("store: this SQLite build has no FTS5; rebuild with `-tags sqlite_fts5` " +
			"(Makefile GO_TAGS; docs/sqlite-setup.md; ADR-0026)")
	}
	return nil
}
