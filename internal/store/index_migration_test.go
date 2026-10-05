package store

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/migrations"
)

// TestRepositoryIndexingMigration proves migration 0014 (M5-T8; ADR-0028):
// projects.repository_path is nullable, and indexed_sources enforces its
// per-project uniqueness, status CHECK, and FK to projects.
func TestRepositoryIndexingMigration(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()
	if err := Migrate(db, migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','code','build things that last')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// repository_path defaults to NULL and accepts a value.
	var path any
	if err := db.QueryRowContext(ctx, `SELECT repository_path FROM projects WHERE id='p1'`).Scan(&path); err != nil {
		t.Fatalf("read repository_path: %v", err)
	}
	if path != nil {
		t.Fatalf("repository_path default = %v, want NULL", path)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE projects SET repository_path='/tmp/repo' WHERE id='p1'`); err != nil {
		t.Fatalf("set repository_path: %v", err)
	}

	// A manifest row round-trips.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO indexed_sources (project_id, relpath, lang, size, mtime_unix, source_hash, chunks)
		VALUES ('p1','main.go','go',12,1700000000,'abc',2)`); err != nil {
		t.Fatalf("insert manifest row: %v", err)
	}

	// Duplicate (project_id, relpath) violates the PK.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO indexed_sources (project_id, relpath, lang, size, mtime_unix, source_hash)
		VALUES ('p1','main.go','go',1,1,'def')`); err == nil {
		t.Error("duplicate (project_id, relpath) accepted, want PK violation")
	}

	// Unknown status violates the CHECK.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO indexed_sources (project_id, relpath, lang, size, mtime_unix, source_hash, status)
		VALUES ('p1','other.go','go',1,1,'def','weird')`); err == nil {
		t.Error("unknown status accepted, want CHECK violation")
	}

	// Unknown project violates the FK.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO indexed_sources (project_id, relpath, lang, size, mtime_unix, source_hash)
		VALUES ('nope','x.go','go',1,1,'def')`); err == nil {
		t.Error("unknown project accepted, want FK violation")
	}
}
