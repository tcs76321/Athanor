# M5-T6 Plan — Temp 0.0 compaction + minimal memory-consolidation driver

**ADR:** [0025-compaction.md](adr/0025-compaction.md) ·
**Acceptance (ROADMAP §M5-T6):** "Compaction determinism test: same input →
same output across runs; temp asserted 0.0" (§10.3). **Gate G5** closes here:
its byte-exact-swap arm (T2/T3) and assembly arm (T5) are already green;
"no compaction runs above Temp 0.0" is this task's arm.

**Scope:** the compaction mechanism (deterministic + semantic), its
content-addressed store, the `security`-persona adapter, and a **minimal**
daydream driver that is the first real caller. The full §17 Daydream Engine,
§17.3 DaydreamLog, and OS AC/battery gating are M7-T1/T2; reading compacted
memos into §11.2 §9 (tier 5) is M6.

## Commit sequence

Each commit: stage → `make check` → staged diff → one-line `M5-T6.x:`
message → human signs.

### T6.1 — ADR-0025 + this plan (docs)

Lock: content-addressed determinism (D1), the §10.3 matrix as executable policy
with Division as the safe default (D2), layering (D3), the dedicated
`CompactStore` + migration 0012 (D4), the `security`/0.0 constants (D5), the
minimal driver's gates/cadence/bounds and its two real sources (D6), no
truncation (D7), and the deferrals (M7 full daydream + AC watcher, M6 tier-5
read, dormant `recovery.max_context_compactions`).

### T6.2 — `internal/prompt/compact.go` + tests

- `CompactDeterministicSystemV1`, `CompactSemanticSystemV1` (versioned §11.1
  Internal-Runtime prompts; the symbol name carries the version).
- `CompactDeterministicVersion`, `CompactSemanticVersion` template-version
  strings (part of the content-address key).
- `CompactMessages(kind, profile, sourceLabel, content) []llm.Message` — pure.
- Tests: identical input → byte-identical messages; kind selects the version;
  the header carries profile + source metadata; the version constant matches
  the symbol.

### T6.3 — `internal/mce/compact.go` + migration 0012 (the Gate-G5 arm)

- `Profile` (`EpistemicType`, `TemporalState`), `Treatment`/`TreatmentFor`
  (§10.3 matrix), `MemoryItem`, `Compactor` seam, `CompactionResult`.
- `const CompactionPersona = "security"`, `CompactionTemperature = 0.0`.
- `NewCompactStore(st)`, `CompactMemory` (cache-aware), `HasSource`, `HasJob`.
- `migrations/0012_compacted_memory.sql`; bump `store_test.go` to version 12 +
  add the table; append `"0012"` to the two `migrationsExcept` lists.
- **The acceptance test lands here** (`TestCompactionDeterminism`): the table
  in §"Acceptance test table".

### T6.4 — `internal/mce/consolidate.go` + tests

- `MemorySource` interface, `Consolidator`, `RunOnce(ctx, limit)` returning
  `ConsolidationResult` (processed/compacted/cached/skipped/failed, byte
  counts).
- Tests with a fake source: catches up; respects `limit`; skipped/failed
  counted; a second run over a static source is all-cache; an empty source is a
  no-op.

### T6.5 — `cmd/athanor`: `security` adapter + wiring

- `compaction_adapter.go`: `mceCompactor` over `llm.RoleSecurity`,
  `Temperature: mce.CompactionTemperature`, messages from
  `prompt.CompactMessages`, full output (no truncation); asserts
  `cfg.ContextEngine.CompactionTemperature == 0.0`.
- `mce_wiring.go`: `mceRuntime` gains `CompactStore` + `Compactor`; `startMCE`
  constructs both; `serve.go` logs them.
- Adapter test pins persona == `security` and Temperature == 0.0.
- Re-prove **Gate G1** and **Gate G2** with `-count=1`.

### T6.6 — `cmd/athanor/daydream.go` + minimal driver + sources

- Gates: `power.daydream_on_idle` ∧ profile `AllowDaydreaming` ∧
  `!killSwitch.Frozen()` ∧ idle (`job.Repository.Active` empty).
- Cadence `power.idle_resume_after` (floor 1m); one pass per tick bounded by
  `power.daydream_max_wall_time_minutes` and `defaultConsolidationBatch = 20`.
- Sources: `terminalJobSource` (event log; `log`+`episodic`; deterministic) and
  `acceptedDocSource` (accepted `document` artifacts;
  `documentation`+`archival`; semantic). Both skip already-compacted inputs.
- New read APIs: `job.Repository.Terminal(ctx, limit)`,
  `artifact.Store.ListAccepted(ctx, limit)`.
- One `daydream` audit event per pass with the §17.3 counters; loop stops on
  daemon shutdown.
- Tests: each gate blocks the pass; idle+allowed runs one pass; the event is
  emitted; cancellation stops the loop.

### T6.7 — close-out (docs)

CHANGELOG M5-T6 entry (commits + **Gate G5 closed**); ROADMAP M5 status row
and the Gate-G5 gates-table row; README MCE/daydream note; ADR-0025
"Implemented" note; cross-references in ADR-0021 §10 and ADR-0023 §2.

## Acceptance test table (`internal/mce`)

| Row | Setup | Expectation |
|---|---|---|
| flaky compactor | a fake returning a different string per call; same item twice | identical bytes; compactor called **once**; 2nd `Cached=true` |
| restart | compact → close → reopen the same DB → compact | identical bytes; no 2nd call |
| template version | same item, two `template_version`s | two rows, two calls (§11.1) |
| kind separation | same content, deterministic vs semantic profile | two rows |
| input change | one content byte differs | new `input_hash` → miss → 2nd call |
| storage invariant | `INSERT` with `temperature = 0.5` | CHECK fails |
| audit | cache hit vs miss | hit: no `memory_compacted`; miss: exactly one |
| not compactable | `code` profile | `ErrNotCompactable` |
| matrix | `TreatmentFor` over all §10.3 rows + unlisted | listed rows compact; the rest Division |
| prompt purity | `CompactMessages` twice | byte-identical messages |
| adapter | `cmd` test | persona `security`, `Temperature == 0.0`, output untruncated |
| driver gates | config off / interactive / frozen / busy | no pass; one `daydream` event only when idle+allowed |
| driver bounds | > batch items available | ≤ `defaultConsolidationBatch` processed per pass |

## Migration 0012 checklist

- `migrations/0012_compacted_memory.sql` — table + indexes + `touch_updated_at`
  trigger, matching 0009 conventions.
- `internal/store/store_test.go` — version `11 → 12`; add `compacted_memory` to
  `wantTables`.
- `internal/store/enum_migration_test.go`,
  `internal/store/job_migration_test.go` — append `"0012"` to the
  `migrationsExcept(...)` lists.
- `internal/mce/store_test.go` — `newStore` version guard stays `>= 9`.

## Risks / notes

- **Determinism must be documented as content-addressed caching**, or a future
  reader will remove the cache; the flaky-compactor row is the falsifiable
  guard.
- **`input_hash` completeness**: kind, template version, persona, and profile
  are all in the key (rows three and four pin this).
- **Driver safety**: off by default, gated four ways, bounded batch + wall
  time, and cache-backed so a repeat pass makes no model call. Sources never
  mutate the audit log or artifacts.
- **Layering**: the two production `MemorySource`s live in `cmd/`;
  `internal/mce` stays `internal/llm`-free, so Gate G2 rule 1's intent holds.
- No new dependency, no route change, one migration, one background goroutine.
- One commit per logical change; one `-m`; GPG-signed by the human.
