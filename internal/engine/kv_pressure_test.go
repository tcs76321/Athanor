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
	"github.com/tcs76321/athanor/internal/store"
)

// fakeEvictor is the M5-T4 Evictor seam; M5-T5 replaces it with the
// MCE-backed adapter. It records calls so tests can prove warn never
// evicts and critical always tries.
type fakeEvictor struct {
	mu    sync.Mutex
	calls int
	freed int
	err   error
}

func (f *fakeEvictor) Evict(ctx context.Context, jobID string, pressure float64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.freed, f.err
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
		power.NewPowerManager(nil), e.runner, ev)
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

// TestKVCacheCriticalWithoutEvictorPauses proves the 95% arm with no
// eviction seam wired (M5-T4 production wiring, ADR-0022 §5): nothing
// can relieve the pressure, so the job pauses instead of sending a call
// whose context is spent.
func TestKVCacheCriticalWithoutEvictorPauses(t *testing.T) {
	criteria := oversizedCriteria(1000)
	P, persona := measuredPrompt(t, criteria)

	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.ContextEngine.SimpleFloor = 512
		// 5% headroom → pressure ≈ 0.952, just past critical.
		shrinkWindow(cfg, persona, P+P/20)
	})
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", criteria)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StatePaused {
		t.Fatalf("job state = %s, want paused (critical with no evictor)", j.State)
	}

	criticals, sendable := 0, 0
	for _, r := range pressureRows(t, e, jobID) {
		if r.Action == "critical" {
			criticals++
		} else if r.Action != "floor_breach" {
			sendable++
		}
	}
	if criticals == 0 {
		t.Fatal("no kv_cache_pressure row with action critical")
	}
	if sendable != e.ollama.calls {
		t.Errorf("pressure rows implying a send = %d, chat calls = %d", sendable, e.ollama.calls)
	}
	v, ok := violationRow(t, e, jobID)
	if !ok || v["trigger"] != "kv_cache_monitor" {
		t.Fatalf("violation row = %v, want trigger kv_cache_monitor", v)
	}
}

// TestKVCacheCriticalEvictsAndProceeds proves the 95% arm with the seam
// wired: the engine asks the evictor to flush the lowest-priority tier,
// records the freed tokens, and proceeds — the freed budget lands in
// the next assembly (M5-T5).
func TestKVCacheCriticalEvictsAndProceeds(t *testing.T) {
	criteria := oversizedCriteria(1000)
	P, persona := measuredPrompt(t, criteria)

	ev := &fakeEvictor{freed: 4096}
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.ContextEngine.SimpleFloor = 512
		shrinkWindow(cfg, persona, P+P/20)
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
		t.Fatalf("job state = %s, want completed (eviction relieved the pressure)", j.State)
	}
	if ev.callCount() == 0 {
		t.Fatal("evictor never invoked at critical pressure")
	}
	criticals, evicted := 0, 0
	for _, d := range inferenceEvents(t, e, jobID) {
		switch d["event"] {
		case "kv_cache_pressure":
			if d["action"] == "critical" {
				criticals++
			}
		case "kv_cache_evicted":
			evicted++
			if got, want := d["tokens_freed"], float64(4096); got != want {
				t.Errorf("tokens_freed = %v, want %v", got, want)
			}
		}
	}
	if criticals == 0 {
		t.Fatal("no critical pressure row on a run sized for critical")
	}
	if evicted != criticals {
		t.Errorf("kv_cache_evicted rows = %d, want one per critical call (%d)", evicted, criticals)
	}
}

// TestKVCacheEvictionErrorFailsJob proves eviction failures do not
// silently proceed: an error from the seam fails the job's phase (the
// engine's standard fail-loud path) rather than sending the prompt.
func TestKVCacheEvictionErrorFailsJob(t *testing.T) {
	criteria := oversizedCriteria(1000)
	P, persona := measuredPrompt(t, criteria)

	ev := &fakeEvictor{err: errors.New("dormant store unavailable")}
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.ContextEngine.SimpleFloor = 512
		shrinkWindow(cfg, persona, P+P/20)
	})
	e.withEvictor(t, ev)

	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.", criteria)
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
