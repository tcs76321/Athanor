package main

import (
	"database/sql"
	"encoding/csv"
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

// TestCheckArtifactTextPrefersBestCandidate pins the ADR-0092 scoring rule:
// non-code checks score the best candidate even when the engine accepted a
// weaker artifact; only when no candidate is readable do they use the accepted
// artifact.
func TestCheckArtifactTextPrefersBestCandidate(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE artifacts (id TEXT PRIMARY KEY, job_id TEXT, kind TEXT, created_at TEXT);
		CREATE TABLE evaluation_records (job_id TEXT, artifact_id TEXT, score REAL);
		INSERT INTO artifacts VALUES ('a1','j1','proposal','2026-01-01');
		INSERT INTO evaluation_records VALUES ('j1','a1',0.9);
	`); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "artifacts", "a1"), []byte("best"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, text := checkArtifactText(db, stateDir, "j1", "accepted", "accepted text")
	if id != "a1" || text != "best" {
		t.Errorf("scored = %q/%q, want a1/best", id, text)
	}

	// No readable candidate: fall back to the accepted artifact.
	id, text = checkArtifactText(db, stateDir, "j2", "accepted", "accepted text")
	if id != "accepted" || text != "accepted text" {
		t.Errorf("fallback = %q/%q, want the accepted artifact", id, text)
	}
}

// TestLoadCodeTestOutcome pins the ADR-0092 code label: the probe reads the
// engine's `tests_ran` events, counts candidates, and passes when any
// candidate's frozen test passed.
func TestLoadCodeTestOutcome(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE events (id INTEGER PRIMARY KEY, job_id TEXT, category TEXT, data_json TEXT);
		INSERT INTO events (job_id, category, data_json) VALUES
			('j1','jobs','{"event":"tests_ran","candidate":0,"passed":false,"command":"python -c bad"}'),
			('j1','jobs','{"event":"tests_ran","candidate":1,"passed":true,"command":"python -c good"}'),
			('j1','jobs','{"event":"transition","to":"evaluating"}');
	`); err != nil {
		t.Fatal(err)
	}
	out, ok := loadCodeTestOutcome(db, "j1")
	if !ok {
		t.Fatal("tests_ran events not found")
	}
	if out.Ran != 2 || out.Passed != 1 {
		t.Errorf("outcome = %+v, want 2 ran / 1 passed", out)
	}
	if out.Command != "python -c bad" {
		t.Errorf("command = %q, want the first recorded command", out.Command)
	}

	m := jobMetrics{Archetype: "code", JobID: "j1"}
	m.fillCodeTest(db)
	if m.CodeTestPassed == nil || !*m.CodeTestPassed {
		t.Fatalf("code test label = %v, want true", m.CodeTestPassed)
	}
	if m.CodeTestTotal != 2 || m.CodeTestPassedCount != 1 {
		t.Errorf("counts = %d/%d, want 2/1", m.CodeTestPassedCount, m.CodeTestTotal)
	}

	// A non-code row never gets a code-test label.
	nonCode := jobMetrics{Archetype: "document", JobID: "j1"}
	nonCode.fillCodeTest(db)
	if nonCode.CodeTestPassed != nil {
		t.Error("non-code row picked up a code-test label")
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

// TestWriteCSVShape pins the flat export: every row must have the same number
// of cells as the header, including the ADR-0092 code-test columns.
func TestWriteCSVShape(t *testing.T) {
	pass := true
	path := filepath.Join(t.TempDir(), "results.csv")
	if err := writeCSV(path, []jobMetrics{{
		GoalName: "x-code-synthetic", Archetype: "code", Tier: "X", Arm: "full",
		ModelLabel: "m1", State: "failed", CodeTestPassed: &pass, PassSource: "code_test",
	}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want header + 1", len(rows))
	}
	if len(rows[0]) != len(rows[1]) {
		t.Errorf("header has %d cells, row has %d", len(rows[0]), len(rows[1]))
	}
}
