package store

import (
	"database/sql"
	"testing"

	"github.com/tcs76321/athanor/migrations"
)

// openMigrated opens a temp store and applies every migration, so the
// retrieval tables added by migration 0013 exist.
func openMigrated(t *testing.T) *Store {
	t.Helper()
	s, _ := openTemp(t)
	if err := Migrate(s.DB(), migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// TestFTS5CompactedMemoryStaysInSync proves the migration-0013 triggers
// keep compacted_memory_fts aligned with compacted_memory across insert,
// update, and delete. An external-content FTS5 table stores no copy of the
// text, so a missing trigger arm shows up here as index drift.
func TestFTS5CompactedMemoryStaysInSync(t *testing.T) {
	db := openMigrated(t).DB()

	if _, err := db.Exec(`
		INSERT INTO compacted_memory
		    (id, kind, profile_type, profile_state, input_hash, template_version,
		     persona, temperature, source_bytes, compacted_bytes, content)
		VALUES ('cm-1', 'deterministic', 'log', 'episodic', 'hash-1', 'v1',
		        'security', 0.0, 10, 20, 'alpha bravo charlie')`); err != nil {
		t.Fatalf("insert memo: %v", err)
	}
	if got := ftsCount(t, db, `SELECT COUNT(*) FROM compacted_memory_fts WHERE compacted_memory_fts MATCH 'bravo'`); got != 1 {
		t.Fatalf("after insert, bravo hits = %d, want 1", got)
	}

	if _, err := db.Exec(`UPDATE compacted_memory SET content = 'delta echo foxtrot' WHERE id = 'cm-1'`); err != nil {
		t.Fatalf("update memo: %v", err)
	}
	if got := ftsCount(t, db, `SELECT COUNT(*) FROM compacted_memory_fts WHERE compacted_memory_fts MATCH 'bravo'`); got != 0 {
		t.Errorf("after update, stale term bravo hits = %d, want 0", got)
	}
	if got := ftsCount(t, db, `SELECT COUNT(*) FROM compacted_memory_fts WHERE compacted_memory_fts MATCH 'echo'`); got != 1 {
		t.Errorf("after update, echo hits = %d, want 1", got)
	}

	if _, err := db.Exec(`DELETE FROM compacted_memory WHERE id = 'cm-1'`); err != nil {
		t.Fatalf("delete memo: %v", err)
	}
	if got := ftsCount(t, db, `SELECT COUNT(*) FROM compacted_memory_fts WHERE compacted_memory_fts MATCH 'echo'`); got != 0 {
		t.Errorf("after delete, echo hits = %d, want 0", got)
	}
}

// TestFTS5DormantIndexStaysInSync is the same proof for dormant_index_fts
// (the Dormant Index summaries).
func TestFTS5DormantIndexStaysInSync(t *testing.T) {
	db := openMigrated(t).DB()

	if _, err := db.Exec(`
		INSERT INTO context_chunks
		    (id, source_relpath, source_hash, lang, kind, byte_start, byte_end,
		     line_start, line_end, content_hash, content)
		VALUES ('chunk-1', 'a/b.go', 'src-1', 'go', 'ast', 0, 3, 1, 1, 'ch-1', x'616263')`); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO dormant_index (chunk_id, summary, summary_status) VALUES ('chunk-1', 'parses config', 'ready')`); err != nil {
		t.Fatalf("insert index row: %v", err)
	}
	if got := ftsCount(t, db, `SELECT COUNT(*) FROM dormant_index_fts WHERE dormant_index_fts MATCH 'config'`); got != 1 {
		t.Fatalf("after insert, config hits = %d, want 1", got)
	}

	if _, err := db.Exec(
		`UPDATE dormant_index SET summary = 'walks the dag', summary_status = 'ready' WHERE chunk_id = 'chunk-1'`); err != nil {
		t.Fatalf("update index row: %v", err)
	}
	if got := ftsCount(t, db, `SELECT COUNT(*) FROM dormant_index_fts WHERE dormant_index_fts MATCH 'config'`); got != 0 {
		t.Errorf("after update, stale term config hits = %d, want 0", got)
	}
	if got := ftsCount(t, db, `SELECT COUNT(*) FROM dormant_index_fts WHERE dormant_index_fts MATCH 'walks'`); got != 1 {
		t.Errorf("after update, walks hits = %d, want 1", got)
	}
}

// TestMemoryEmbeddingsShape pins the migration-0013 embeddings table: the
// (owner_id, model) key, the positive-dimension CHECK, and the closed
// source_kind set.
func TestMemoryEmbeddingsShape(t *testing.T) {
	db := openMigrated(t).DB()

	if _, err := db.Exec(
		`INSERT INTO memory_embeddings (owner_id, source_kind, model, dim, vector, content_hash)
		 VALUES ('cm-1', 'memo', 'nomic-embed-text', 3, x'0000803f', 'h1')`); err != nil {
		t.Fatalf("insert embedding: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO memory_embeddings (owner_id, source_kind, model, dim, vector, content_hash)
		 VALUES ('cm-1', 'memo', 'nomic-embed-text', 3, x'0000803f', 'h2')`); err == nil {
		t.Error("duplicate (owner_id, model) insert succeeded; the primary key is missing")
	}
	if _, err := db.Exec(
		`INSERT INTO memory_embeddings (owner_id, source_kind, model, dim, vector, content_hash)
		 VALUES ('cm-2', 'memo', 'm', 0, x'00', 'h3')`); err == nil {
		t.Error("dim = 0 insert succeeded; the CHECK (dim > 0) is missing")
	}
	if _, err := db.Exec(
		`INSERT INTO memory_embeddings (owner_id, source_kind, model, dim, vector, content_hash)
		 VALUES ('cm-3', 'other', 'm', 1, x'00', 'h4')`); err == nil {
		t.Error("source_kind = 'other' insert succeeded; the CHECK is missing")
	}
}

// ftsCount runs a one-column COUNT query against an FTS5 table.
func ftsCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}
