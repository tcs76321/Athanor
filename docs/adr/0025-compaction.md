# ADR 0025 — Temp 0.0 compaction and the minimal memory-consolidation driver (M5-T6)

**Status:** Accepted · **Date:** 2026-09-30 · **Refs:** ARCHITECTURE §10.1,
§10.2, §10.3, §11.1, §11.2, §17.1–§17.3, §24, §28; ROADMAP M5-T6; ADR-0021
(chunk store, Dormant Index, swap), ADR-0022 (KV-cache monitoring), ADR-0023
(assembly priority), ADR-0019 (dependency inversion), ADR-0003 (SQLite/CGO
single connection).

## Context

M5-T2–T5 built the lossless half of the MCE: byte-exact division, the Dormant
Index, `context_swap`, the KV-cache monitor, and §10.5 assembly. §10.3's other
half — **compaction**, the *only* lossy operation in the MCE — is unbuilt.
§10.3 pins the whole treatment matrix and one hard invariant: *"all context
manipulation operations strictly use Temperature 0.0"*. Two mechanisms are
named: **Deterministic Compaction** (`log`/`test_output` + `episodic`; extract
exact error codes, stack traces, return values) and **Semantic Compaction**
(`conversation`/`brainstorming` + `archival`, `documentation` + `episodic`/
`archival`; extract explicit decisions, constraints, derived rules, key facts,
API signatures).

Two facts shape the design:

1. **Ollama at 0.0 is not bit-reproducible** (M3-T7-c: quantization, KV-cache
   state, batching). "Same input → same output across runs" cannot rest on the
   model.
2. **No episodic/archival memory producer exists yet** (CorrectionRecords are
   M6; DaydreamLog is M7). The real, available sources are the append-only
   **event log** (§28) and **accepted documentation artifacts**.

## Decision

### 1. Determinism is content-addressed, not model-reproducible

Every compaction is keyed by
`input_hash = sha256(kind ‖ template_version ‖ persona ‖ profile ‖ sha256(content))`.
The **first** compaction of an input is canonical; every later request returns
the stored bytes **without a model call**. Three layers make this falsifiable:

- `prompt.CompactMessages` is pure — identical input yields byte-identical
  messages.
- `compacted_memory` is `UNIQUE (kind, input_hash)`; a hit is byte-identical
  and LLM-free. A racing double-miss resolves with
  `INSERT … ON CONFLICT DO NOTHING` + re-read, so the first writer's bytes win.
- Temp 0.0 is enforced in three places: `config/validate.go` (already rejects
  non-zero), the table's `CHECK (temperature = 0.0)`, and the adapter, which
  constructs the request at `mce.CompactionTemperature` and never routes
  through `ResolveTemperature` (compaction is not a §13.1 phase).

Rejected alternatives: trusting the model to be deterministic (not guaranteed);
re-calling every time (drift risk — the corruption §10.3 exists to prevent).

### 2. The §10.3 matrix is executable policy; the default is lossless

| §10.2 profile | Treatment | Kind |
|---|---|---|
| `code` (any temporal state) | Division | — |
| `test_output` + `active`/`recent` | Division | — |
| `log` + `episodic` | Compaction | `deterministic` |
| `test_output` + `episodic` | Compaction | `deterministic` |
| `conversation` + `archival` | Compaction | `semantic` |
| `documentation` + `episodic`/`archival` | Compaction | `semantic` |
| any unlisted combination | Division | — |

`TreatmentFor` is pure and table-tested. `code` never compacts (§10.1: source
code is divided, never summarized). An unlisted combination falls back to
Division — "when unsure, divide" is the only direction that cannot lose bytes.
`CompactMemory` derives the kind from the profile and returns
`ErrNotCompactable` when the matrix says "divide", so a caller cannot force a
lossy pass over full-fidelity content.

### 3. Layering — pure prompts, an LLM-free store, inverted adapters

- `internal/prompt` owns the two versioned §11.1 Internal-Runtime prompts
  (`CompactDeterministicSystemV1`, `CompactSemanticSystemV1`) and the pure
  `CompactMessages`. A template change is a new symbol and a new
  `TemplateVersion` string, which is part of the content-address key — so a
  version bump is a new cache row, never a silent edit of an old one.
- `internal/mce` owns `Profile`, `TreatmentFor`, `MemoryItem`, the `Compactor`
  seam, the dedicated `CompactStore`, and the `Consolidator`. It **does not
  import `internal/llm`** (ADR-0021 §2), so `internalapi` cannot gain a model
  path through the MCE.
- `cmd/athanor` owns the `security`-persona adapter, the driver's source
  selection, and scheduling (ADR-0019 inversion).

### 4. Storage — a dedicated `CompactStore` + migration 0012

Compaction gets its own store type (`mce.NewCompactStore(st)`), keeping
`ChunkStore` focused on division/swap. Migration 0012 adds `compacted_memory`:
one row per distinct compacted input, a deterministic content-derived id
(ADR-0021 §5), `UNIQUE (kind, input_hash)`, and `CHECK (temperature = 0.0)` as
the storage-layer enforcement of §10.3. Source data is never deleted — the
event log is append-only and trigger-protected (§28) and artifact bytes are
untouched; compaction produces a *derived memo*, never a replacement.

### 5. The `Compactor` seam and the `security` persona

`mce.Compactor.Compact(ctx, item, kind) (string, error)` is the LLM-free seam
(the `Summarizer` precedent). `mce.CompactionPersona = "security"` and
`mce.CompactionTemperature = 0.0` are the §10.3 constants; the adapter is
required to use them and the adapter test pins both.

### 6. The minimal daydream driver (one §17.1 action)

A power/idle-gated background loop in `cmd/athanor/daydream.go`, launched by
`serve.go`, runs **one bounded consolidation pass per tick**:

- **Gates (all must hold):** `power.daydream_on_idle`; the active profile
  allows daydreaming (`AllowDaydreaming`, false in the default interactive
  profile); the kill switch is not frozen; the job queue is idle
  (`job.Repository.Active` empty).
- **Cadence:** `power.idle_resume_after` (floor 1m). **Bound:** one pass ≤
  `power.daydream_max_wall_time_minutes` and ≤ `defaultConsolidationBatch = 20`
  items.
- **Sources:** terminal-job `events` batches (`log`+`episodic`, deterministic)
  and accepted `document` artifacts (`documentation`+`archival`, semantic).
  Both skip inputs that already have a `compacted_memory` row, and the cache
  makes a repeat pass free.
- **Audit:** one `daydream` event per pass with §17.3-shaped counters.

Output is memory, never an artifact, so §17.2's "draft-only" holds by
construction. The other five §17.1 actions, the `DaydreamLog` table/object, and
OS AC/battery gating are M7-T1/T2.

### 7. No silent truncation

The adapter returns the model's full output. `source_bytes` and
`compacted_bytes` are recorded; nothing is cut.

## Deferrals

- Full Daydream Engine (§17.1 remaining actions, §17.3 DaydreamLog) and the OS
  AC/battery watcher — M7-T1/T2.
- Reading compacted memos into §11.2 §9 / tier 5 (episodic) — M6 (ADR-0023 §2).
- `recovery.max_context_compactions` stays dormant: it names an engine-side
  per-job compaction budget that does not exist. T6's bound is the daydream
  wall-time + batch constant.

## Consequences

**Positive**

- §10.3 becomes executable: the only lossy MCE operation is bounded by content
  addressing, a persistent cache, and a three-layer Temp-0.0 guard.
- Gate G5 closes: compaction determinism joins the byte-exact swap and the
  assembly ladder.
- The event log finally has a bounded, derived condensation path; the raw audit
  trail is untouched.

**Negative / accepted**

- One migration and one new background goroutine in the daemon.
- The driver is off by default (interactive profile); enabling it is the
  operator's (or M6 UI's) call.
- `compacted_memory` grows with distinct inputs; retention is future work.

## Caveats

- Content addressing makes re-compaction free but means a *changed* input is a
  new row; near-duplicate memory is not deduplicated. Accepted; pruning is M7.
- The semantic source compacts accepted documentation artifacts; code artifacts
  are never selected (the matrix classifies `code` as Division).
- `docs/m5-t6-plan.md` is the execution plan; this ADR is the decision record.
  If reality disagrees, the plan changes first and this ADR gains an
  "Implemented" note (the ADR-0021/0022/0023 pattern).
