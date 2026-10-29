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

// TestLoadComparisonDiagnostics pins the P0 diagnostic reader: the guard
// inputs and the last reason line are what make an "external check passed but
// engine failed" row explainable.
func TestLoadComparisonDiagnostics(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE events (id INTEGER PRIMARY KEY, job_id TEXT, category TEXT, data_json TEXT);
		INSERT INTO events (job_id, category, data_json) VALUES
			('j1','jobs','{"event":"transition","to":"comparing"}'),
			('j1','jobs','{"event":"comparison","winner":"none","reasons":["judge declined","downgraded from new to none"],"guard_pre_winner":"new","guard_post_winner":"none","guard_threshold":0.7,"has_previous":false,"final_record_passed":true,"final_record_confidence":0.0,"final_record_better":false,"final_verifier_applied":1,"final_verifier_hard":false,"final_verifier_hard_passed":true}');
	`); err != nil {
		t.Fatal(err)
	}
	c, ok := loadComparisonDiagnostics(db, "j1")
	if !ok {
		t.Fatal("comparison event not found")
	}
	if c.GuardPreWinner != "new" || c.GuardPostWinner != "none" {
		t.Errorf("guard = %s -> %s, want new -> none", c.GuardPreWinner, c.GuardPostWinner)
	}
	if c.FinalRecordPassed == nil || !*c.FinalRecordPassed {
		t.Error("final record pass was not carried")
	}
	if len(c.Reasons) != 2 || c.Reasons[1] != "downgraded from new to none" {
		t.Errorf("reasons = %v", c.Reasons)
	}
}
