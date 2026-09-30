# M5-T4 Plan — KV-cache monitoring: pre-call pressure, 85%/95% triggers, floor-breach pause

**ADR:** [0022-kv-cache-monitoring.md](adr/0022-kv-cache-monitoring.md) ·
**Acceptance (ROADMAP §M5-T4):** "Simulated pressure tests trigger correct
action at each threshold; floor breach never silently truncates" (§10.4).

**Scope boundary (ADR-0022 §7):** T4 delivers the monitor, trigger
decisions, floor-breach pause, and a nilable `Evictor` seam. The
MCE-backed eviction adapter and the prompt integration (Dormant Index
tier, swap-suggestion injection) are M5-T5 — the same deferral shape
`git_operation` used in M3-T5 → M3-T7.

## Commit sequence

Each commit: stage → `make check` → staged diff → one-line `M5-T4.x:`
message → human signs.

### T4.1 — ADR-0022 + this plan (docs)

Lock before code: pure `Assess` in `internal/mce` (§10.4 is MCE-owned;
package stays LLM-free), `max_context = persona.ContextTarget`, strictly-`>`
trigger semantics with `>=` floor breach, `inference`-category events
(`kv_cache_pressure`, `kv_cache_evicted`), nilable `Evictor` seam
(ToolRunner precedent), the `llm.Check`/`Assess` division of labor, and
the T5 deferral list.

### T4.2 — `internal/mce/pressure.go` + simulated pressure tests

- `Action` (`none|warn|critical|floor_breach`), `Assessment`
  (`Pressure`, `Action`, `Recommendation`), `Assess(activeTokens,
  maxContext int, ce config.ContextEngine) Assessment`.
- Rules: `maxContext <= 0` → floor_breach (fail closed);
  `activeTokens >= maxContext` → floor_breach; `>` critical threshold →
  critical; `>` warning threshold → warn; else none.
- **The acceptance criterion is this table test**: simulated pressures
  trigger the correct action at each threshold, including exact-boundary
  rows (exactly 0.85 → none, exactly 0.95 → warn, exactly 1.0 →
  floor_breach) and the fail-closed rows (zero/negative max context).
- Thresholds come from `config.ContextEngine` so operators can move them.

### T4.3 — engine wiring: `Evictor` seam + category audit + pre-call gate

- `internal/engine`: `Evictor` interface (nilable; nil or zero-freed at
  `critical` → treat as floor breach), `auditCat` helper (category
  parameter; existing `audit` keeps `jobs`).
- `call()` gate, after `prompt.Assemble`, before `Chat`:
  1. `Assess(res.TotalToken, persona.ContextTarget, e.cfg.ContextEngine)`.
  2. Always append `kv_cache_pressure` (category `inference`).
  3. `warn` → audit only in T4 (suggestion injection is T5).
  4. `critical` → invoke the seam; if nothing freed (or nil seam),
     fall through to the floor-breach path.
  5. `floor_breach` → transition to `paused`, append
     `context_floor_violation` with the §12.3 recommendation, return
     `ErrPaused` — the request is never sent (Ollama would silently
     truncate).
- Engine tests with the fake client: fabricated oversized prompts drive
  pause/warn/critical/nil-seam/seam-freed paths; existing tests stay
  green (typical prompts are far below threshold → `none`).
- Re-prove Gate G1 (engine surface + import change).

### T4.4 — close-out (docs)

CHANGELOG entry, ROADMAP status (M5-T4 done; the T4 trigger closes the
ADR-0021 §10 deferral on the trigger side — prompt integration remains
T5), README only if the tool surface changed (it does not).

## Risks / notes

- No new dependency, no migration, no new route — nothing for Gate G2
  route coverage; G1 re-proof is the only gate ceremony.
- Estimate drift (4 bytes/token): actuals recorded in the same row;
  if actuals routinely exceed thresholds while estimates sit below,
  revisit the estimator before the thresholds (ADR-0022 caveat).
- `inference` is already an enabled event category in
  `config.example.yaml` — no config change.
- One commit per logical change; one `-m`; GPG-signed by the human.