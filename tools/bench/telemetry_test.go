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
		`{"event":"kv_cache_pressure","action":"warn"}`,
		`{"event":"kv_cache_pressure","action":"critical"}`,
		`{"event":"context_floor_violation"}`,
		`{"event":"git_committed"}`,
		`{"event":"git_commit_failed"}`,
		`{"event":"dormant_index_sourced","rows":4}`,
		`{"event":"working_set_sourced","chunks":3,"bytes":900}`,
		`{"event":"memory_injected","hits":2,"bytes":400}`,
		`{"event":"memory_compacted","source_bytes":1000,"compacted_bytes":250}`,
		`{"event":"learned_candidate_selection"}`,
		`{"event":"replanned"}`,
		`{"event":"observation_read"}`,
		`{"event":"clarification_requested"}`,
		`{"event":"memory_remembered"}`,
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
	if tel.KVPressure != 2 || tel.KVWarn != 1 || tel.KVCritical != 1 || tel.ContextFloors != 1 {
		t.Errorf("kv/floors = %d/%d/%d/%d, want 2/1/1/1",
			tel.KVPressure, tel.KVWarn, tel.KVCritical, tel.ContextFloors)
	}
	if tel.GitCommitted != 1 || tel.GitCommitFailed != 1 {
		t.Errorf("git = %d/%d, want 1/1", tel.GitCommitted, tel.GitCommitFailed)
	}
	if tel.DormantRows != 4 || tel.WorkingSetChunks != 3 || tel.WorkingSetBytes != 900 {
		t.Errorf("working set = rows %d chunks %d bytes %d, want 4/3/900",
			tel.DormantRows, tel.WorkingSetChunks, tel.WorkingSetBytes)
	}
	if tel.MemoryHits != 2 || tel.MemoryBytes != 400 {
		t.Errorf("memory = %d/%d, want 2/400", tel.MemoryHits, tel.MemoryBytes)
	}
	if tel.Compactions != 1 || tel.CompactionSource != 1000 || tel.CompactionBytes != 250 {
		t.Errorf("compaction = %d/%d/%d, want 1/1000/250",
			tel.Compactions, tel.CompactionSource, tel.CompactionBytes)
	}
	if tel.LearnedSelections != 1 || tel.Replans != 1 || tel.Observations != 1 ||
		tel.Clarifications != 1 || tel.MemoryRemembered != 1 {
		t.Errorf("cognitive-loop counters = %+v", tel)
	}
}

// TestSaturatedFirstPass pins the ADR-0116 label: a completed job with no
// repair, reflection, replan, or observation is a ranking row, not loop
// evidence.
func TestSaturatedFirstPass(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, job_id TEXT, category TEXT, data_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	m := jobMetrics{JobID: "j1", State: "completed"}
	m.fillTelemetry(db)
	if !m.Saturated {
		t.Fatal("completed first pass was not marked saturated")
	}
	m = jobMetrics{JobID: "j1", State: "completed"}
	if _, err := db.Exec(`INSERT INTO events (job_id, category, data_json) VALUES ('j1','jobs','{"event":"reflection"}')`); err != nil {
		t.Fatal(err)
	}
	m.fillTelemetry(db)
	if m.Saturated {
		t.Fatal("a job with a reflection was marked saturated")
	}
}

func TestPairedRows(t *testing.T) {
	pass := true
	fail := false
	rows := []jobMetrics{
		{GoalName: "same", Arm: "single", State: "completed", Winner: "new", CheckPass: &pass, TokenCost: 100},
		{GoalName: "same", Arm: "full", State: "completed", Winner: "new", CheckPass: &pass, TokenCost: 300},
		{GoalName: "regressed", Arm: "single", State: "completed", Winner: "new", CheckPass: &pass, TokenCost: 100},
		{GoalName: "regressed", Arm: "full", State: "completed", Winner: "none", CheckPass: &fail, TokenCost: 300},
	}
	got := pairedRows(rows)
	if len(got) != 2 {
		t.Fatalf("pairs = %+v", got)
	}
	byGoal := map[string]pairedRow{}
	for _, p := range got {
		byGoal[p.Goal] = p
	}
	same := byGoal["same"]
	if same.SinglePass != 1 || same.FullPass != 1 || same.QualityRegress {
		t.Fatalf("same = %+v", same)
	}
	if same.TokenMultiple < 2.9 || same.TokenMultiple > 3.1 {
		t.Fatalf("token multiple = %v, want ~3", same.TokenMultiple)
	}
	reg := byGoal["regressed"]
	if !reg.QualityRegress || reg.Regressed != 1 {
		t.Fatalf("regressed = %+v", reg)
	}
}

func TestSequenceRows(t *testing.T) {
	rows := []jobMetrics{
		{GoalName: "seq", Arm: "full", Progress: 0.66, Sequence: []sequenceStep{{}, {}, {}}},
		{GoalName: "single", Arm: "full"},
	}
	got := sequenceRows(rows)
	if len(got) != 1 || got[0].Goal != "seq" || got[0].Steps != 3 {
		t.Fatalf("sequence rows = %+v", got)
	}
}

func TestSaturationCount(t *testing.T) {
	rows := []jobMetrics{
		{State: "completed", Saturated: true},
		{State: "completed"},
		{State: "failed"},
	}
	sat, total := saturationCount(rows)
	if sat != 1 || total != 2 {
		t.Fatalf("saturation = %d/%d, want 1/2", sat, total)
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
