package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestBestCandidateArtifact pins external scoring: the highest-scoring proposal
// is used even when the engine accepted no artifact, with a last-proposal
// fallback for older DBs without evaluation rows.
func TestBestCandidateArtifact(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE artifacts (id TEXT PRIMARY KEY, job_id TEXT, kind TEXT, created_at TEXT);
		CREATE TABLE evaluation_records (job_id TEXT, artifact_id TEXT, score REAL);
		INSERT INTO artifacts VALUES
			('a1','j1','proposal','2026-01-01'),
			('a2','j1','proposal','2026-01-02');
		INSERT INTO evaluation_records VALUES ('j1','a1',0.2),('j1','a2',0.9);
	`); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for id, text := range map[string]string{"a1": "low", "a2": "high"} {
		if err := os.WriteFile(filepath.Join(stateDir, "artifacts", id), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id, text, err := bestCandidateArtifact(db, stateDir, "j1")
	if err != nil {
		t.Fatal(err)
	}
	if id != "a2" || text != "high" {
		t.Errorf("best = %q/%q, want a2/high", id, text)
	}

	// No evaluation rows: fall back to the last proposal (a2).
	if _, err := db.Exec(`DELETE FROM evaluation_records`); err != nil {
		t.Fatal(err)
	}
	id, text, err = bestCandidateArtifact(db, stateDir, "j1")
	if err != nil {
		t.Fatal(err)
	}
	if id != "a2" || text != "high" {
		t.Errorf("fallback = %q/%q, want a2/high", id, text)
	}
}
