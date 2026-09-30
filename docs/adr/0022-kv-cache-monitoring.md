# ADR 0022 — KV-cache monitoring: pre-call pressure assessment and the eviction seam (M5-T4)

**Status:** Accepted · **Date:** 2026-09-29 · **Refs:** ARCHITECTURE §10.4,
§11.2, §12.3, §12.6, §28; ROADMAP M5-T4; ADR-0021 (chunk store, Dormant
Index, swap), ADR-0002 (context floor semantics), ADR-0003 (SQLite/CGO).

## Context

M5-T3 shipped the `context_swap` mechanism and recorded a deferral
(ADR-0021 §10): the engine does not call the tool automatically — the
trigger is M5-T4 (KV-cache 85%/95%) and the prompt integration is M5-T5
(§11.2 tier 3 / Dormant Index). T4 is therefore the *monitoring* half of
§10.4: pre-call token counting, the two pressure triggers, and the
floor-breach pause.

There is a structural tension to resolve first: §10.4's 85%/95% actions
("move oldest non-pinned chunks to Dormant", "force-evict lowest-priority
tier") operate on chunks **inside the prompt**, but chunks only enter
prompts in T5. T4 ships the monitor, the trigger decisions, the
floor-breach protection, and a nilable eviction seam; T5 fills the seam
and integrates the prompt. This is the same deferral shape
`git_operation` used in M3-T5 → M3-T7.

## Decision

### 1. Pressure assessment is a pure function in `internal/mce`

`Assess(activeTokens, maxContext int, ce config.ContextEngine) Assessment`
— table-tested, no LLM, no I/O, mirroring the `DecideWinner` pure-function
pattern. It lives in `internal/mce` because §10.4 is MCE-owned (the
engine-imports-mce direction is allowed; `internal/mce` stays LLM-free
per ADR-0021 §2, and `Assess` touches neither).

```go
type Action string // "none" | "warn" | "critical" | "floor_breach"

type Assessment struct {
    Pressure       float64 // activeTokens / maxContext
    Action         Action
    Recommendation string // non-empty on warn/critical/floor_breach
}
```

Rules (fail closed):

- `maxContext <= 0` → `floor_breach` (an unset or nonsensical window is a
  configuration error, never an excuse to send an unbounded prompt).
- `activeTokens >= maxContext` → `floor_breach`: the request cannot fit
  the window. Ollama silently truncates an oversized prompt at `num_ctx`,
  so sending it would violate the "never silently truncates" invariant —
  the engine must not send.
- `pressure > kv_cache_critical_threshold` (default 0.95) → `critical`.
- `pressure > kv_cache_warning_threshold` (default 0.85) → `warn`.
- otherwise → `none`.

### 2. Trigger semantics

Strictly `>` for the 85%/95% thresholds (§10.4's own wording:
"active_tokens > 85% of max_context") and `>=` for the floor breach (a
window filled to exactly 100% has no room for even one completion token).
Exact-boundary rows are pinned in the table tests.

### 3. `max_context` is the persona's `ContextTarget`

§12.3: `effective_context = min(max_context, model_architecture_maximum,
persona_context_target)`. The persona's `ContextTarget` is what
`llm.Client` sets `num_ctx` to, so it is the operative window for that
call. Ollama-reported actuals (`Response.PromptTokens`,
`prompt_eval_count`) are recorded in the same audit row as calibration
evidence but are only available post-call, so the trigger decision uses
the deterministic pre-call estimate (`prompt.Result.TotalToken`,
~4 bytes/token) — behavior stays unit-testable without a live Ollama.

### 4. Events (no migration; open event-name set)

- `kv_cache_pressure` — emitted on **every** engine LLM call, category
  `inference` (§28: "context calculations, KV cache pressure"): pressure,
  action, estimated tokens, max context, phase, persona.
- `kv_cache_evicted` — emitted when the eviction seam frees anything (T5).
- The engine's audit helper gains a category parameter; existing callers
  keep the `jobs` default.
- `context_floor_violation` (existing event name, `jobs` category) is
  reused for the monitor's floor-breach pause so post-mortems query one
  name for both the §12.6 pre-assembly gate and the §10.4 per-call gate.

### 5. The eviction seam is nilable (ToolRunner precedent)

```go
type Evictor interface {
    // Evict frees context pressure by flushing lowest-priority
    // §10.5 tiers to Dormant. Returns the tokens freed.
    Evict(ctx context.Context, jobID string, pressure float64) (int, error)
}
```

A nil `Evictor` is valid configuration: the engine short-circuits
eviction and treats `critical` with a nil seam (or a seam that freed
nothing) as a floor breach — pausing rather than sending a prompt that
would be silently truncated. T4's production wiring passes nil; T5 builds
the MCE-backed adapter over `ChunkStore`/`ActiveSet`. At `warn`, T4 only
audits: the §10.4 "issue `context_swap` suggestion to the LLM" is prompt
work that lands with T5's tier integration.

### 6. Division of labor with `llm.Check` (§12.6)

`llm.Check` remains the pre-assembly feasibility gate (persona target vs
archetype floor; what the *window* must be). `Assess` is the per-call
pressure gate (estimated tokens vs the resolved window; what goes *in*
it). Both pause; neither truncates. They are complementary, not
redundant.

### 7. What T4 does not do

- No chunks in prompts, no Dormant Index section, no swap-suggestion
  injection (T5).
- No MCE-backed `Evictor` (T5; the seam is nil in production wiring).
- No Ollama `/api/show` probing — recorded actuals suffice.

**Implemented (M5-T4, `f28fadf` + `79b494e`):** `internal/mce/pressure.go`
holds the pure `Assess` with a 15-row simulated-pressure table
(`pressure_test.go`); the engine gate lives in `internal/engine/phases.go`
(`call()` + `pauseForPressure`), the seam in `internal/engine/engine.go`
(`Evictor`, nil at the `cmd/athanor` call site), and the trigger paths are
proven end-to-end in `internal/engine/kv_pressure_test.go`.

## Consequences

**Positive**

- An oversized prompt can no longer reach Ollama: the invariant
  "Athanor never silently truncates context" becomes *enforced* per call,
  not just aspiration.
- Pressure evidence accrues in the EventLog for every call, giving T5+
  real calibration data (estimate vs `prompt_eval_count`).
- T5 has a fixed seam and fixed event names to build against.

**Negative / accepted**

- The ~4 bytes/token estimate is coarse; pressure rows may mis-state by
  some margin. Accepted: actuals are recorded alongside for calibration,
  and the strict-`>` thresholds err conservative.
- A legitimately huge prompt now pauses the job instead of truncating.
  That is the intended §10.4 behavior; operators see the recommendation
  row and can shrink task scope or change models.

## Caveats

- Until T5, the 85% arm is evidence-only (no suggestion injected).
- The `Evictor` signature may evolve when T5 implements it (tier
  selection, `context_swap` interplay); expect an ADR-0021-style
  amendment, not silent drift.
- Estimate drift: if real jobs show `prompt_eval_count` routinely above
  the critical threshold while estimates sit below it, the estimator —
  not the thresholds — should be revisited first.