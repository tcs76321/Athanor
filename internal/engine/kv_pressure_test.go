package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/store"
)

// fakeEvictor is the M5-T4 `Evictor` seam; M5-T5 filled it with the real
// ladder (`NewLadderEvictor`). It records calls and returns a scripted
// eviction, so tests can prove warn never evicts, critical always tries,
// and "nothing evictable" pauses.
type fakeEvictor struct {
	mu    sync.Mutex
	calls int
	// freed is the token weight reported for the evicted tier; the tier
	// itself is the lowest the caller's weights offer, so a scripted
	// eviction stays consistent with the ladder.
	freed int
	// none makes the fake report "nothing evictable", which is the
	// floor-breach path.
	none bool
	err  error
}

func (f *fakeEvictor) Evict(_ context.Context, _ string,
	weights map[prompt.Tier]int, suppressed []prompt.Tier) (Eviction, error) {

	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Eviction{}, f.err
	}
	if f.none {
		return Eviction{}, nil
	}
	tier, tokens, ok := prompt.EvictNext(weights, suppressed)
	if !ok {
		return Eviction{}, nil
	}
	if f.freed > 0 {
		tokens = f.freed
	}
	return Eviction{Tiers: []prompt.Tier{tier}, FreedTokens: tokens}, nil
}

func (f *fakeEvictor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// submitWithCriteria creates a project + task with an acceptance-criteria
// list the engine will assemble into every prompt. Criteria are the
// pressure test's control surface: `oversizedCriteria` inflates the
// assembled estimate deterministically (EstimateTokens ≈ 4 bytes/token).
func (e *testEnv) submitWithCriteria(t *testing.T, archetype, goal string, criteria []string) (jobID string) {
	t.Helper()
	submitSeq++
	_, task, err := e.projects.Create(context.Background(),
		fmt.Sprintf("pressure-%s-%d", archetype, submitSeq), archetype, goal, criteria)
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.jobs.Create(context.Background(), task.ID, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}

// oversizedCriteria returns criteria carrying about `tokens` estimated
// tokens (prompt.EstimateTokens is ~4 bytes per token).
func oversizedCriteria(tokens int) []string {
	return []string{strings.Repeat("a", tokens*4)}
}

// withEvictor rebuilds the env's engine with an eviction seam. The
// engine is stateless at this point (no job has run), so swapping the
// whole value keeps the harness helpers intact.
func (e *testEnv) withEvictor(t *testing.T, ev Evictor) {
	t.Helper()
	registry, err := llm.NewRegistry(e.cfg.Personas)
	if err != nil {
		t.Fatal(err)
	}
	e.eng = New(e.cfg, e.db, e.jobs, e.projects, e.artifacts, evaluation.NewRepo(e.db),
		llm.NewClient(e.cfg.Inference.OllamaURL, nil), registry, e.freezer,
		power.NewPowerManager(nil), e.runner, ev, nil, nil)
}

// pressureRow is the decoded `kv_cache_pressure` audit row.
type pressureRow struct {
	Event       string  `json:"event"`
	Phase       string  `json:"phase"`
	Persona     string  `json:"persona"`
	Action      string  `json:"action"`
	ActiveToken int     `json:"active_tokens"`
	MaxContext  int     `json:"max_context"`
	Pressure    float64 `json:"pressure"`
}

// pressureRows returns every `kv_cache_pressure` row for a job, in
// append order, filtered by the §28.1 `inference` category — which is
// itself the category assertion.
func pressureRows(t *testing.T, e *testEnv, jobID string) []pressureRow {
	t.Helper()
	recs, err := e.db.QueryEvents(context.Background(),
		store.EventFilter{Category: "inference", JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	var rows []pressureRow
	for _, r := range recs {
		var p pressureRow
		if err := json.Unmarshal([]byte(r.DataJSON), &p); err != nil {
			t.Fatalf("decode %s: %v", r.DataJSON, err)
		}
		if p.Event == "kv_cache_pressure" {
			rows = append(rows, p)
		}
	}
	return rows
}

// violationRow returns the first `context_floor_violation` row for a job.
func violationRow(t *testing.T, e *testEnv, jobID string) (map[string]any, bool) {
	t.Helper()
	recs, err := e.db.QueryEvents(context.Background(),
		store.EventFilter{Category: "context", JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		var d map[string]any
		if err := json.Unmarshal([]byte(r.DataJSON), &d); err != nil {
			t.Fatalf("decode %s: %v", r.DataJSON, err)
		}
		if d["event"] == "context_floor_violation" {
			return d, true
		}
	}
	return nil, false
}

// shrinkWindow sets the named persona's context window (num_ctx), the
// quantity §10.4 pressure is measured against.
func shrinkWindow(cfg *config.Config, persona string, target int) {
	switch persona {
	case llm.RoleTall:
		cfg.Personas.Tall.ContextTarget = target
	case llm.RoleMain:
		cfg.Personas.Main.ContextTarget = target
	case llm.RoleSecurity:
		cfg.Personas.Security.ContextTarget = target
	case llm.RoleAlternative:
		cfg.Personas.Alternative.ContextTarget = target
	case llm.RoleWide:
		cfg.Personas.Wide.ContextTarget = target
	}
}

// TestKVCacheFloorBreachPausesBeforeSend proves the acceptance half
// "floor breach never silently truncates": a prompt that cannot fit the
// persona's window pauses the job, and the oversized prompt is never
// sent to the model (the pressure rows that would imply a send match
// the chat-call count exactly).
func TestKVCacheFloorBreachPausesBeforeSend(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		// Keep the §12.6 gate satisfied (floor ≤ targeted window) so
		// only the §10.4 monitor can pause this run.
		cfg.ContextEngine.SimpleFloor = 512
		cfg.Personas.Tall.ContextTarget = 16384
		cfg.Personas.Main.ContextTarget = 2048
	})
	// Criteria large enough that the diverging prompt (main, 2048)
	// cannot fit, small enough that planning (tall, 16384) is untouched.
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", oversizedCriteria(5000))
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StatePaused {
		t.Fatalf("job state = %s, want paused (kv-cache floor breach)", j.State)
	}

	rows := pressureRows(t, e, jobID)
	breaches, sendable := 0, 0
	for _, r := range rows {
		if r.Action == "floor_breach" {
			breaches++
		} else {
			sendable++
		}
	}
	if breaches == 0 {
		t.Fatal("no kv_cache_pressure row with action floor_breach")
	}
	if sendable != e.ollama.calls {
		t.Errorf("pressure rows implying a send = %d, chat calls = %d: an oversized prompt reached the model",
			sendable, e.ollama.calls)
	}

	v, ok := violationRow(t, e, jobID)
	if !ok {
		t.Fatal("no context_floor_violation row for the kv-cache pause")
	}
	if got := v["trigger"]; got != "kv_cache_monitor" {
		t.Errorf("violation trigger = %v, want kv_cache_monitor", got)
	}
	if rec, _ := v["recommendation"].(string); !strings.Contains(rec, "never silently truncates") {
		t.Errorf("recommendation = %q, want the §12.3 escalation text", rec)
	}
}

// TestKVCacheWarnAuditsAndProceeds proves the 85% arm: pressure above
// the warning threshold is recorded and the call proceeds (T4 audits
// only; the swap suggestion lands with M5-T5), and the eviction seam is
// never touched at warn.
func TestKVCacheWarnAuditsAndProceeds(t *testing.T) {
	criteria := oversizedCriteria(1000)
	P, persona := measuredPrompt(t, criteria)

	ev := &fakeEvictor{freed: 1 << 20}
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.ContextEngine.SimpleFloor = 512
		// 10% headroom over the largest prompt → pressure ≈ 0.909,
		// inside (warning, critical].
		shrinkWindow(cfg, persona, P+P/10)
	})
	e.withEvictor(t, ev)

	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", criteria)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("job state = %s, want completed (warn must not block)", j.State)
	}

	warns, above := 0, 0
	rows := pressureRows(t, e, jobID)
	for _, r := range rows {
		if r.Action == "warn" {
			warns++
		}
		if r.Action == "critical" || r.Action == "floor_breach" {
			above++
		}
	}
	if warns == 0 {
		t.Fatalf("no warn row; pressures: %+v", rows)
	}
	if above != 0 {
		t.Errorf("%d rows above warn on a run sized for warn", above)
	}
	if got := ev.callCount(); got != 0 {
		t.Errorf("evictor called %d times at warn, want 0 (T4 warn audits only)", got)
	}
	if len(rows) != e.ollama.calls {
		t.Errorf("pressure rows = %d, chat calls = %d", len(rows), e.ollama.calls)
	}
}

// measuredPrompt runs a job under the default (roomy) windows and
// returns the largest assembled prompt estimate and the persona that
// produced it — the baseline the trigger tests shrink a window against.
func measuredPrompt(t *testing.T, criteria []string) (tokens int, persona string) {
	t.Helper()
	e := newEnv(t)
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", criteria)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("baseline run state = %s, want completed", j.State)
	}
	recs, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		var d struct {
			Event     string `json:"event"`
			Persona   string `json:"persona"`
			Estimated int    `json:"estimated_prompt_tokens"`
		}
		if err := json.Unmarshal([]byte(r.DataJSON), &d); err != nil {
			continue
		}
		if d.Event == "llm_call" && d.Estimated > tokens {
			tokens, persona = d.Estimated, d.Persona
		}
	}
	if tokens == 0 || persona == "" {
		t.Fatal("baseline run produced no llm_call rows to measure")
	}
	// Every call is audited before it is attempted, so the pressure
	// rows and the chat calls must agree exactly.
	if got, want := len(pressureRows(t, e, jobID)), e.ollama.calls; got != want {
		t.Fatalf("pressure rows = %d, want %d (one per call)", got, want)
	}
	return tokens, persona
}

// measuredPhasePrompt runs a criteria-less job under the default (roomy)
// windows and returns the estimated prompt size of one phase plus that
// phase's persona. A criteria-less task matters: the planning call is then
// pinned-only (tiers 1–2), which is the quantity the §10.4 critical arm
// actually keys on once M5-T5's ceiling is in place (ADR-0023 §3).
func measuredPhasePrompt(t *testing.T, phase string) (tokens int, persona string) {
	t.Helper()
	e := newEnv(t)
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", nil)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("baseline run state = %s, want completed", j.State)
	}
	recs, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		var d struct {
			Event     string `json:"event"`
			Phase     string `json:"phase"`
			Persona   string `json:"persona"`
			Estimated int    `json:"estimated_prompt_tokens"`
		}
		if err := json.Unmarshal([]byte(r.DataJSON), &d); err != nil {
			continue
		}
		if d.Event == "llm_call" && d.Phase == phase {
			tokens, persona = d.Estimated, d.Persona
		}
	}
	if tokens == 0 || persona == "" {
		t.Fatalf("baseline run produced no llm_call row for phase %q", phase)
	}
	return tokens, persona
}

// inferenceEvents returns every `inference`-category row for a job as
// decoded maps (the raw shape, for events beyond kv_cache_pressure).
func inferenceEvents(t *testing.T, e *testEnv, jobID string) []map[string]any {
	t.Helper()
	recs, err := e.db.QueryEvents(context.Background(),
		store.EventFilter{Category: "inference", JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		var d map[string]any
		if err := json.Unmarshal([]byte(r.DataJSON), &d); err != nil {
			t.Fatalf("decode %s: %v", r.DataJSON, err)
		}
		out = append(out, d)
	}
	return out
}

// TestKVCacheCriticalWithoutEvictorPauses proves the narrowed §10.4
// critical arm in the M5-T5 world: the assembler's own ladder evicts
// evictable tiers first (ADR-0023 §3), so `critical` can only mean the
// pinned tiers alone exceed the ceiling — where nothing is evictable and
// the gate must pause rather than send a prompt Ollama would truncate.
func TestKVCacheCriticalWithoutEvictorPauses(t *testing.T) {
	pinned, persona := measuredPhasePrompt(t, llm.PhasePlanning)

	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.ContextEngine.SimpleFloor = 512
		// ~3% headroom over the pinned-only prompt → pressure just past
		// critical, with no evictable tier to relieve it.
		shrinkWindow(cfg, persona, pinned+pinned/40)
	})
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", nil)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StatePaused {
		t.Fatalf("job state = %s, want paused (critical with nothing evictable)", j.State)
	}
	if got := e.ollama.callCount(); got != 0 {
		t.Errorf("chat calls = %d, want 0 (an oversized prompt must never be sent)", got)
	}
	criticals := 0
	for _, r := range pressureRows(t, e, jobID) {
		if r.Action == "critical" {
			criticals++
		}
	}
	if criticals == 0 {
		t.Fatal("no kv_cache_pressure row with action critical")
	}
	v, ok := violationRow(t, e, jobID)
	if !ok || v["trigger"] != "kv_cache_monitor" {
		t.Fatalf("violation row = %v, want trigger kv_cache_monitor", v)
	}
}

// shrinkAllWindows sets every persona's context window to the same value
// (the §10.5 ceiling is per-persona, so a pressure test must move them
// together or it only constrains one phase).
func shrinkAllWindows(cfg *config.Config, window int) {
	cfg.Personas.Tall.ContextTarget = window
	cfg.Personas.Main.ContextTarget = window
	cfg.Personas.Security.ContextTarget = window
	cfg.Personas.Alternative.ContextTarget = window
	cfg.Personas.Wide.ContextTarget = window
}

// TestKVCacheAssemblyEvictsInsteadOfReachingCritical is the M5-T5
// supersession of T4's "critical force-evicts and proceeds": pressure is
// now relieved at assembly time by the §10.5 ladder, so a job under
// pressure still completes, the eviction is recorded, the gate never had
// to go critical, and no suppression outlives the terminal state.
//
// The window is sized between the pinned-only planning prompt and the
// largest prompt any phase assembles: planning must fit (or the job
// pauses for the right reason), and the largest phase must evict.
func TestKVCacheAssemblyEvictsInsteadOfReachingCritical(t *testing.T) {
	pinned, _ := measuredPhasePrompt(t, llm.PhasePlanning)
	largest, _ := measuredPrompt(t, nil)
	if largest <= pinned {
		t.Skipf("baseline phases add no evictable content (pinned=%d, largest=%d)", pinned, largest)
	}
	// ceiling = 0.95·W lands strictly between the two:
	//   pinned < (pinned+largest)/2 < largest
	window := (pinned + largest) / 2 * 100 / 95

	e := newEnvWithCfg(t, func(cfg *config.Config) {
		// Keep the §12.6 floor below the shrunken window so only the
		// §10.5 ladder and the §10.4 gate act in this test.
		cfg.ContextEngine.SimpleFloor = 128
		shrinkAllWindows(cfg, window)
	})
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", nil)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("job state = %s, want completed (the ladder relieved the pressure)", j.State)
	}

	assembled, evictedAny, criticals := 0, 0, 0
	for _, d := range inferenceEvents(t, e, jobID) {
		t.Logf("row event=%v phase=%v action=%v evicted=%v suppressed=%v fits=%v",
			d["event"], d["phase"], d["action"], d["evicted_tiers"], d["suppressed_tiers"], d["fits"])
		switch d["event"] {
		case "kv_cache_assembled":
			assembled++
			if names, _ := d["evicted_tiers"].([]any); len(names) > 0 {
				evictedAny++
			}
		case "kv_cache_pressure":
			if d["action"] == "critical" {
				criticals++
			}
		}
	}
	if assembled == 0 {
		t.Fatal("no kv_cache_assembled row")
	}
	if evictedAny == 0 {
		t.Errorf("no assembly evicted a tier across %d assembled rows (window=%d)", assembled, window)
	}
	if criticals != 0 {
		t.Errorf("gate went critical %d times; the ladder should relieve pressure first", criticals)
	}
	if got := suppressedRow(t, e, jobID); got != "" {
		t.Errorf("suppression row survived terminal state: %q", got)
	}
}

// suppressedRow reads the raw system_state suppression value for a job.
func suppressedRow(t *testing.T, e *testEnv, jobID string) string {
	t.Helper()
	var raw string
	if err := e.db.DB().QueryRow(
		`SELECT value FROM system_state WHERE key = ?`, "context:evicted:"+jobID).Scan(&raw); err != nil {
		return ""
	}
	return raw
}

// TestKVCacheEvictionErrorFailsJob proves eviction failures do not
// silently proceed: an error from the seam fails the job's phase (the
// engine's standard fail-loud path) rather than sending the prompt.
func TestKVCacheEvictionErrorFailsJob(t *testing.T) {
	pinned, persona := measuredPhasePrompt(t, llm.PhasePlanning)

	ev := &fakeEvictor{err: errors.New("dormant store unavailable")}
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.ContextEngine.SimpleFloor = 512
		shrinkWindow(cfg, persona, pinned+pinned/40)
	})
	e.withEvictor(t, ev)

	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", nil)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateFailed {
		t.Fatalf("job state = %s, want failed (eviction error is fail-loud)", j.State)
	}
	if got := ev.callCount(); got != 1 {
		t.Errorf("evictor calls = %d, want 1", got)
	}
}
