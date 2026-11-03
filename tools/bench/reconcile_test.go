package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestReconcileUpdatesADR0092Labels pins the late-outcome path: when the live
// collector missed a strategy outcome, reconcile must recover the winner and
// keep engine_accepted / pass_source consistent with it. A code row's test
// label still wins over engine acceptance (ADR-0092).
func TestReconcileUpdatesADR0092Labels(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "athanor.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE strategy_outcomes (
			job_id TEXT, result TEXT, score REAL, evaluator_confidence REAL,
			token_cost INTEGER, wall_time_ms INTEGER, retries INTEGER,
			reflection_loops INTEGER
		);
		CREATE TABLE events (id INTEGER PRIMARY KEY, job_id TEXT, category TEXT, data_json TEXT);
		CREATE TABLE evaluation_records (job_id TEXT, artifact_id TEXT, score REAL);
		CREATE TABLE artifacts (id TEXT PRIMARY KEY, job_id TEXT, kind TEXT, created_at TEXT);
		INSERT INTO strategy_outcomes VALUES ('j1','accepted_new',1.0,0.9,100,1000,0,0);
	`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	passed := true
	metrics := []jobMetrics{{
		JobID: "j1", Archetype: "code", State: "completed", LabelVersion: benchLabelVersion,
		CodeTestPassed: &passed, PassSource: "engine_accept",
	}}
	n, err := reconcileMetrics(dir, metrics)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled = %d, want 1", n)
	}
	m := metrics[0]
	if m.Winner != "new" {
		t.Errorf("winner = %q, want new", m.Winner)
	}
	if !m.EngineAccepted {
		t.Error("engine_accepted was not updated from the reconciled winner")
	}
	if m.PassSource != "code_test" {
		t.Errorf("pass source = %q, want code_test", m.PassSource)
	}
	if !jobPassed(m) {
		t.Error("a code row with a passing test label must pass even after reconcile")
	}
}
