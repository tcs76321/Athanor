package main

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestFillTelemetry pins the ADR-0073 §3 reducer: the per-phase/per-persona
// token counts and the engine's lever counters are read from the job's event
// log, and an unknown event is ignored rather than corrupting the reduction.
func TestFillTelemetry(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, job_id TEXT, category TEXT, data_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"event":"llm_call","phase":"diverging","persona":"main","model":"gemma4:12b-mlx","prompt_tokens":100,"completion_tokens":50}`,
		`{"event":"llm_call","phase":"diverging","persona":"main","model":"gemma4:12b-mlx","prompt_tokens":100,"completion_tokens":50}`,
		`{"event":"llm_call","phase":"evaluating","persona":"security","model":"granite4.2:3b","prompt_tokens":40,"completion_tokens":10}`,
		`{"event":"self_refine"}`,
		`{"event":"reflection"}`,
		`{"event":"divergence_reroll"}`,
		`{"event":"difficulty_hint"}`,
		`{"event":"kv_cache_pressure"}`,
		`{"event":"context_floor_violation"}`,
		`{"event":"git_committed"}`,
		`{"event":"git_commit_failed"}`,
		`{"event":"some_future_event_the_engine_may_add"}`,
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO events (job_id, category, data_json) VALUES ('j1', 'jobs', ?)`, r); err != nil {
			t.Fatal(err)
		}
	}

	m := jobMetrics{JobID: "j1"}
	m.fillTelemetry(db)
	tel := m.Telemetry
	if tel == nil {
		t.Fatal("Telemetry is nil")
	}
	if tel.Calls != 3 {
		t.Errorf("calls = %d, want 3", tel.Calls)
	}
	if tel.PromptTokens != 240 || tel.CompletionTokens != 110 {
		t.Errorf("tokens = %d/%d, want 240/110", tel.PromptTokens, tel.CompletionTokens)
	}
	if tel.ByPhaseCalls["diverging"] != 2 || tel.ByPhaseTokens["diverging"] != 300 {
		t.Errorf("diverging = %d calls / %d tokens, want 2/300",
			tel.ByPhaseCalls["diverging"], tel.ByPhaseTokens["diverging"])
	}
	if tel.ByPersonaTokens["main"] != 300 || tel.ByPersonaTokens["security"] != 50 {
		t.Errorf("persona tokens = %v, want main=300 security=50", tel.ByPersonaTokens)
	}
	if len(tel.Models) != 2 {
		t.Errorf("models = %v, want 2 distinct", tel.Models)
	}
	if tel.RepairLoops != 1 || tel.ReflectLoops != 1 || tel.Rerolls != 1 || tel.CandidateRaise != 1 {
		t.Errorf("lever counters wrong: %+v", tel)
	}
	if tel.KVPressure != 1 || tel.ContextFloors != 1 {
		t.Errorf("kv/floors = %d/%d, want 1/1", tel.KVPressure, tel.ContextFloors)
	}
	if tel.GitCommitted != 1 || tel.GitCommitFailed != 1 {
		t.Errorf("git = %d/%d, want 1/1", tel.GitCommitted, tel.GitCommitFailed)
	}
}

// TestFillTelemetryNoJobIDIsSafe pins that a job row with no id (an error
// before execution) does not panic and yields an empty telemetry.
func TestFillTelemetryNoJobIDIsSafe(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, job_id TEXT, category TEXT, data_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	m := jobMetrics{}
	m.fillTelemetry(db)
	if m.Telemetry == nil || m.Telemetry.Calls != 0 {
		t.Errorf("empty telemetry = %+v, want non-nil zero", m.Telemetry)
	}
}

func TestRenderTelemetryTables(t *testing.T) {
	m := mk("a", "full", 1, 0.9, 0.9, 0.5, "new", 100)
	m.ModelLabel = "gemma12b"
	m.Telemetry = &jobTelemetry{
		Calls: 3, PromptTokens: 240, CompletionTokens: 110,
		ByPhaseCalls:  map[string]int{"diverging": 2, "evaluating": 1},
		ByPhaseTokens: map[string]int{"diverging": 300, "evaluating": 50},
	}
	out := renderTelemetryTables([]jobMetrics{m})
	for _, want := range []string{"Telemetry — per model", "gemma12b", "calls by phase", "diverging"} {
		if !strings.Contains(out, want) {
			t.Errorf("telemetry table missing %q\n%s", want, out)
		}
	}
	noTel := mk("a", "full", 1, 0.9, 0.9, 0.5, "new", 1)
	if renderTelemetryTables([]jobMetrics{noTel}) != "" {
		t.Error("expected empty render when no row carries telemetry")
	}
}
