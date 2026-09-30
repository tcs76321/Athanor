# ADR 0023 — Context assembly priority queue and the tier eviction ladder (M5-T5)

**Status:** Accepted · **Date:** 2026-09-29 · **Refs:** ARCHITECTURE §10.1,
§10.4, §10.5, §11.1, §11.2, §12.3, §25, §28; ROADMAP M5-T5; ADR-0002
(context floor semantics), ADR-0019 (dependency inversion), ADR-0021 (chunk
store, Dormant Index, swap), ADR-0022 (KV-cache monitor and the `Evictor`
seam), ADR-0003 (SQLite/CGO single connection).

## Context

M5-T4 monitors KV-cache pressure and pauses when a prompt cannot fit, but it
sends whatever the naive §11.2 assembler built: sections 1–6 plus phase
extras, no chunks, no Dormant Index, no budget. §10.5 defines the missing
half — a priority queue over seven tiers that *"fills the available KV
cache"* and, on overflow, evicts *"strictly bottom-up (7 → 6 → 5 → 4) before
any full-fidelity content (tiers 1–3) is touched"*. §10.5 also states the
invariant this ADR must not break: the ordering governs **budgeting and
eviction only**; prompt construction order is §11.2's, separately.

T5 therefore delivers: the tier model and ladder, the rendering that finally
puts the §11.2 §7 active chunk, §10 Dormant Index, and §12 candidate
artifacts into the prompt, the persisted eviction state that makes §10.4's
85% arm ("move oldest non-pinned, non-critical chunks to Dormant. Provide
Dormant Index.") real, and the production implementation of the `Evictor`
seam T4 left nil (ADR-0022 §5).

Reality check on content: no production ingestion call site exists yet
(`IngestFile` is exercised only by tests; repository indexing is M5-T8), so
tiers 3 and 6 are populated by `context_swap` (T3) and by tests until T8.
Tiers 4 (CorrectionRecords) and 5 (episodic) have no producer until M6. The
ladder is nevertheless complete and unit-proven now, which is exactly what
the acceptance criterion asks for ("Overflow evicts tiers 7→6→5→4 only;
tiers 1–3 never evicted (unit-tested)").

## Decision

### 1. Layering — pure assembler, orchestrating engine, inverted adapters

- `internal/prompt` owns the tier model, the ceiling math, the ladder, and
  the rendering. It stays pure and MCE-free: the engine hands it plain data
  (strings and small structs), never a store.
- `internal/engine` owns orchestration: it reads tier content, computes the
  ceiling, persists eviction state, and implements the `Evictor` ladder.
- The MCE-backed reads live in `cmd/` behind engine interfaces (the
  ADR-0019 inversion the project already uses for `ToolRunner`,
  `ContextSwapper`, and the `Summarizer`). `internal/mce` keeps its
  "no `internal/llm`" property; `internalapi` still never imports
  `internal/mce`.

### 2. Tier ↔ §11.2 mapping (explicit, so both orders stay honest)

| §10.5 tier | Renders §11.2 sections | Evictable |
|---|---|---|
| 1 Static System & Security | 1 Static System, 2 Security/Tools, 3 Runtime Policy | never |
| 2 Current Task & Criteria | 4 Project, 5 Task, 6 Acceptance Criteria, 11 User Preferences | never |
| 3 Primary working set | 7 Active Code Division Chunk, **12 Candidate Artifacts** | never |
| 4 CorrectionRecords | 8 | last (M6 producer) |
| 5 Episodic Context | 9 | yes (M6 producer) |
| 6 Dormant Index | 10 | yes |
| 7 Evaluation Instructions & Strategy Notes | 13 Evaluation Instructions, 14 Strategy Notes (15 Interruption Notes, M6) | first |

Two consequences are load-bearing. First, Runtime Policy (§11.2 §3) is tier
1, so the M1-T8.1 synthesis no-preamble instruction and the evaluation
determinism instruction can never be evicted — evicting tier 7 cannot
re-open that regression, and a test pins it. Second, candidate artifacts
move from the `extraInstructions` string (where `phaseSynthesize` and
`phaseEvaluate` currently concatenate them into §11.2 §13) into their own
§11.2 §12 section at tier 3: a phase that *works on* a candidate cannot have
it evicted out from under it.

### 3. The assembly ceiling is the critical threshold × the persona window

`ceiling = int(kv_cache_critical_threshold × persona.ContextTarget)`
(0.95 × `num_ctx` by default, i.e. ~1638 tokens of generation headroom at
32768). §10.4's hard limit is the critical threshold, so the assembler aims
there rather than at the raw window.

Alternatives rejected: filling to 100% leaves no room for the model's own
output and would make T4 pause jobs that are merely full; targeting the
85% warning threshold evicts instructions earlier than the spec asks.
Because the ceiling **is** the critical threshold, the post-assembly T4 gate
reports `warn` at worst whenever tiers 4–7 were evictable, and `critical`
only when tiers 1–3 alone are that large — which is precisely when T4's
pause is the right answer. No new configuration knob is introduced.

### 4. The eviction ladder

Eviction is strictly bottom-up, one populated tier at a time (7 → 6 → 5 →
4), stopping at the first total that fits. The report names exactly which
tiers went and how many tokens they carried, so the audit row is precise
rather than a boolean. Tiers 1–3 are rendered byte-identically regardless of
pressure — eviction never rewrites or truncates full-fidelity content.

When tiers 1–3 alone exceed the ceiling the result is `Fits=false`: the
assembler still renders them (never truncate), the engine proceeds, and the
T4 gate — which sees `critical` — calls the `Evictor`, whose ladder has
nothing left, returns 0, and triggers T4's existing floor-breach pause. One
mechanism, two entry points, no new failure mode.

Construction order never changes: sections are always emitted in §11.2 order
and only their *presence* varies. A test renders an assembly with every
evictable tier suppressed and asserts the §11.2 ordering of what remains.

### 5. Eviction state is persisted, not in-memory

Suppressed tiers live in `system_state` under `context:evicted:<job-id>`
(the `reflect:counter:<job-id>` precedent from M3-T4), as a JSON array of
tier numbers. Persisted because §23.6 restarts and pause/resume must not
resurrect evicted tiers: a resumed job that forgot its evictions would
re-inflate, re-evict, and thrash. The row is cleared when the job reaches a
terminal state, so the table stays bounded (the key-per-job pattern is the
same one the reflection counter already uses).

Suppression is never *lifted* mid-job — no oscillation, no re-eviction
churn. It is set by the assembler's overflow evictions and by the `Evictor`
ladder, and it is reset (not partially relaxed) only when the job ends.
Operators who want a fresh budget start a new job.

### 6. The `Evictor` seam is the ladder (T4's deferral, filled)

ADR-0022 §5 left the seam nil in production and explicitly allowed the
signature to evolve. T5 fills it without changing the signature: "moving a
tier to Dormant" is *suppression* — the bytes already live in the dormant
chunk store from T2/T3, and the tier's 1-line entry appears in the Dormant
Index (§11.2 §10), which is exactly §10.4's 85% arm: move the chunks to
Dormant, provide the index, let the model `context_swap` them back. The
ladder returns the suppressed tier's token weight as `freed`; it returns 0
when only tiers 1–3 remain, preserving T4's pause contract bit for bit.

A model-driven `context_swap` (T3) then changes the stored active pointer,
and the next assembly's tier 3 reads the newly active chunk — the loop
closes without the engine calling the tool itself.

### 7. Read path — a `ContextProvider` seam, `IndexForJob`, migration 0011

`internal/engine` gains a second, focused interface (nil-able, the
`ToolRunner` precedent):

```go
type ContextProvider interface {
    ActiveChunk(ctx context.Context, jobID string) (ChunkText, bool, error)
    DormantIndex(ctx context.Context, jobID string) ([]IndexLine, error)
}
```

implemented in `cmd/athanor` over `mce.ChunkStore` with `SwapContext`'s scope
convention (the job ID, `toolenvelope.ContextSwapRequest.Scope`). A nil
provider means "no MCE": tiers 3 and 6 are empty, which keeps the walking
skeleton and every pre-T5 test byte-identical.

The Dormant Index needs a query the store does not have: `IndexForJob`.
`context_chunks` already carries `project_id`/`job_id` but only indexes
`source_hash` and `project_id`, so migration **0011** adds
`idx_context_chunks_job` (index-only, forward-only, no table change). The
query returns the per-chunk `IndexLine` set: chunk ID, 1-line summary
(`summary_status` respected — pending/failed rows still list with an empty
summary), source path, line range, kind.

### 8. The tool manifest must be honest

`prompt`'s §11.2 §2 text currently asserts "You have NO tools in this
phase". That is true today only because the shipped default is
`job_pod.default_tools: []`; the moment a task declares `allowed_tools` the
prompt contradicts the envelope the server enforces. T5 passes the resolved
envelope (task override when set, else the daemon default, resolved through
the same `toolenvelope.Parse` path the server uses, so the prompt can never
promise a tool the route would 403) into the assembler:

- empty envelope → the current text, **verbatim** (no byte drift);
- non-empty envelope → the actual closed-set manifest, so §10.4's
  "issue a `context_swap` suggestion to the LLM" names a tool the model can
  actually call.

**Implemented refinement (M5-T5.3):** the `context_swap` *invitation* ("one
chunk is active at a time; requesting a dormant chunk flushes the current
one") is rendered with the Dormant Index in §11.2 §10, not in §2. §2 is
tier 1 and is priced before the ladder runs, so an invitation that depends
on whether the index survived would make the tier weights — and therefore
the eviction decision — depend on their own outcome. With the index, the
invitation is present exactly when there is something to point at, and the
ladder math stays exact.

### 9. What T5 does not do

- No tier 4/5 producers (CorrectionRecords, episodic context) — M6.
- No ingestion or repository indexing — M5-T8; tiers 3/6 are populated by
  `context_swap` and tests until then.
- No compaction — M5-T6.
- No new dependency, no route change, no migration beyond the index in 0011.

**Implemented (M5-T5.2–T5.5):** `internal/prompt/{tiers,render}.go` carry the
tier model, ladder, and rendering; `internal/prompt.Assemble` prices every
populated section per tier and emits only the surviving tiers in §11.2
order; `internal/engine/context_tiers.go` holds `ContextProvider`, the
`system_state` suppression state, the ceiling, the honest tool manifest, and
the `Evictor` ladder. One consequence is worth recording because it changed
an M5-T4 test's premise: with the ceiling at the critical threshold, the
assembler evicts *before* the gate can fire, so the §10.4 `critical` arm now
means "the pinned tiers alone are that large" — the seam's empty result is
the pause path, and T4's "force-evict and proceed" branch is unreachable
from a normal job by construction (it remains the seam's contract and is
unit-tested directly). A pressured end-to-end run exercises this: the
evaluating phase evicts tier 7, the suppression persists to the following
calls, and the gate reports `none` throughout.

## Consequences

**Positive**

- §10.5 becomes executable: the window is filled by priority, overflow
  evicts 7→4, and tiers 1–3 are structurally untouchable.
- The prompt finally carries what the MCE was built to hold — the active
  chunk and the Dormant Index — and §10.4's 85% arm has a mechanism
  (suppression + index + swap suggestion) rather than an audit row.
- T4's `Evictor` seam is filled without a signature change, so the M5-T4
  tests and pause contract stay valid.
- Eviction state survives restarts, so §23.6 resume does not thrash.

**Negative / accepted**

- Prompts grow (up to the ceiling) and their content varies with pressure,
  so a byte-comparison of two runs of the same job can differ once tiers are
  populated. Determinism is per-input, not across pressure states — which is
  what a budgeted assembler means.
- Suppression is monotonic within a job: a phase that would fit its
  instructions back in does not get them. Accepted to avoid oscillation; a
  new job starts clean.
- `system_state` gains a row per evicting job (cleared at terminal state).

## Caveats

- The ~4 bytes/token estimate now *decides* evictions rather than only
  warning; `kv_cache_assembled` records per-section estimates next to the
  following `llm_call`'s `prompt_eval_count` so T6+ can calibrate before
  anyone tunes a threshold.
- Tier 3/6 content stays empty in production until M5-T8 ingests a
  workspace; the ladder is proven by tests and by tiers 4–7 content in the
  meantime. This is deliberate and recorded, not an oversight.
- The tool manifest (decision 8) is the first prompt change that reflects
  per-task configuration; if a future tool is added to the closed set, its
  presentation line belongs here too.
- `docs/m5-t5-plan.md` is the execution plan; this ADR is the decision
  record. If implementation reality disagrees, the plan changes first and
  this ADR gains an "Implemented" note (the ADR-0021/0022 pattern).

