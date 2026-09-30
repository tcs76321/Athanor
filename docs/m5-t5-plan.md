# M5-T5 Plan — Context assembly priority queue (tiers 1–7) + the eviction ladder

**ADR:** [0023-context-assembly-priority.md](adr/0023-context-assembly-priority.md) ·
**Acceptance (ROADMAP §M5-T5):** "Overflow evicts tiers 7→6→5→4 only; tiers
1–3 never evicted (unit-tested)" (§10.5). Gate G5's third arm
("assembly-priority unit tests green") turns green here; G5 completes with
T6's compaction determinism.

**Scope:** as decided, one task — assembly core, rendering, the MCE index
query, engine wiring, cmd wiring, close-out. Tier 4/5 producers (M6),
ingestion (T8) and compaction (T6) are explicitly out of scope.

## Commit sequence

Each commit: stage → `make check` → staged diff → one-line `M5-T5.x:`
message → human signs.

### T5.1 — ADR-0023 + this plan + ADR-0022 §5 amendment (docs)

Lock: layering (pure `internal/prompt`, orchestrating engine, `cmd/`
adapters), the tier↔§11.2 mapping table, ceiling =
`kv_cache_critical_threshold × persona.ContextTarget`, the strict ladder and
`Fits=false`, persisted suppression (`system_state`,
`context:evicted:<job-id>`, cleared at terminal state), the `Evictor` =
ladder decision, the `ContextProvider` read path + `IndexForJob`/migration
0011, the honest tool manifest, candidate artifacts moving to §11.2 §12, and
the out-of-scope list. ADR-0022 §5 records that T5 fills the seam without a
signature change.

### T5.2 — `internal/prompt`: tier model + ceiling + ladder + report

- `Tier` (1–7), `Tiers` input struct (chunk, corrections, episodic, dormant
  index lines, strategy notes, candidates, prefs), `Ceiling`, `Suppressed`.
- `EvictionReport{Fits bool, Evicted []Tier, DroppedTokens int}`.
- `Assemble` computes section tokens (existing `EstimateTokens`), applies
  suppression, evicts bottom-up while over the ceiling, and stops at the
  first fit. Pure and deterministic.
- **The acceptance test lands here** (see the table below).

### T5.3 — `internal/prompt`: render the new sections in §11.2 order

- Sections 7 (active chunk), 8 (corrections), 9 (episodic), 10 (Dormant
  Index), 11 (user preferences), 12 (candidate artifacts), 14 (strategy
  notes) — rendered in §11.2 order, omitted when empty (the existing
  `add`-helper contract).
- §11.2 §2 becomes envelope-aware: empty envelope → today's text verbatim;
  non-empty → the closed-set manifest + the `context_swap` line when a tier
  was suppressed.
- Tests: §11.2 order preserved under full suppression; tier-1 safety
  (synthesis no-preamble survives); omission of empty sections;
  determinism; token accounting per section.

### T5.4 — `internal/mce`: `IndexForJob` + migration 0011

- `migrations/0011_context_chunks_job_index.sql`:
  `CREATE INDEX idx_context_chunks_job ON context_chunks(job_id)`.
- `ChunkStore.IndexForJob(ctx, jobID) ([]IndexEntry, error)`, joining
  `dormant_index` with the same shape `IndexForSource` returns; pending and
  failed summaries still list with an empty summary.
- Tests: rows scoped to the job (not the project, not other jobs), empty
  result is not an error, migration idempotency.

### T5.5 — engine wiring

- `ContextProvider` interface (nil-able) + tier input construction:
  active chunk (tier 3), candidate artifacts (tier 3, replacing the
  `extraInstructions` prefix in `phaseSynthesize`/`phaseEvaluate`),
  dormant index (tier 6), evaluation instructions + strategy notes (tier 7,
  from `extraInstructions`).
- Ceiling from config × the persona's `ContextTarget`; envelope from the
  task (`Task.AllowedTools`) or `cfg.JobPod.DefaultTools`.
- Suppression persistence: read/write `context:evicted:<job-id>`; merge the
  report's evictions; clear at terminal state.
- `Evictor` implementation = the ladder over the same state.
- Audit: `kv_cache_assembled` (per-section tokens, evicted tiers, dropped
  tokens, fits) plus the enriched `kv_cache_pressure` row.
- Tests: chunk + index appear in the prompt; overflow evicts 7→6→5→4;
  tiers 1–3 never evicted end-to-end; suppression survives a restart and is
  honored on the next call; terminal state clears it; critical with only
  tiers 1–3 → ladder returns 0 → existing pause; tool manifest matches the
  task envelope; nil provider ⇒ prompts byte-identical to pre-T5 (the
  regression guard).

### T5.6 — `cmd/athanor`: adapters + boot wiring

- `ContextProvider` over `mce.ChunkStore` (scope = job ID; `IndexForJob` for
  the index) and the tier `Evictor`; wired in `serve.go` next to the T4
  seam (which stops being `nil`).
- Boot test asserting construction; Gate G1 and G2 re-proven with
  `-count=1` (`internal/` import surface changed).

### T5.7 — close-out (docs)

- CHANGELOG (M5-T5 entry, commits, Gate G5 arm), ROADMAP M5 status row
  (T5 done; G5's assembly-priority arm green, G5 itself open until T6),
  README (assembly bullet; `Evictor` no longer nil), ADR-0022 §5
  "Implemented (M5-T5)" note, ADR-0023 implemented note if reality moved.

## Acceptance test table (`internal/prompt`)

| Row | Setup | Expectation |
|---|---|---|
| 7 fits | over ceiling by tier-7's weight only | only 7 evicted; 1–6 rendered |
| 7+6 | 7 alone insufficient | 6 then 7 evicted |
| 7+6+5 | two insufficient | 5, 6, 7 evicted |
| 7+6+5+4 | three insufficient | 4 evicted too; tiers 1–3 intact |
| none left | tiers 1–3 alone over ceiling | `Fits=false`; nothing evicted; 1–3 byte-identical |
| exactly at ceiling | total == ceiling | no eviction |
| suppression input | tier 6 pre-suppressed | 6 absent, ladder starts at 7/5 |
| order | full suppression | remaining sections in §11.2 order |
| determinism | same input twice | byte-identical output |

## Risks / notes

- **Prompt content changes** (tool manifest, candidate section) are the
  sharpest edge; the `default_tools: []` + nil-provider case must stay
  byte-identical, which is asserted rather than assumed.
- **Estimate drift now decides evictions**: `kv_cache_assembled` records the
  per-section estimates so the following `llm_call`'s `prompt_eval_count`
  can calibrate them (thresholds are config, not code).
- **`system_state` growth**: one row per evicting job, cleared at terminal
  state.
- One commit per logical change; one `-m`; GPG-signed by the human.
