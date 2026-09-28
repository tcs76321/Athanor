# ADR 0021 — MCE chunk store, Dormant Index, and the active/dormant swap (M5-T2, M5-T3)

**Status:** Accepted · **Date:** 2026-09-27 · **Refs:** ARCHITECTURE §10,
§11.2, §23, §25; ROADMAP M5-T2, M5-T3; ADR-0020 (division strategy —
Accepted this date), ADR-0003 (SQLite/CGO single connection), ADR-0015
(airlock pipelines), ADR-0019 (dependency-inversion precedent), ADR-0008
(per-job tokens); spike `spikes/m5-t1-division/`, findings
[docs/probes/m5-t1-division.md](../probes/m5-t1-division.md).

## Context

M5-T1 raced three division strategies and proved the byte-partition
contract (P1–P5) on a 196-file corpus; ADR-0020 recommended a hybrid and
left the tree-sitter dependency as an explicit **human** decision
(AGENTS.md: "Adding a dependency is a project decision"). That decision
is now **accepted: strategy A, the hybrid.**

M5-T2 turns division into a persisted capability — the §10.1 "Division
Engine" plus the **chunk store** and **Dormant Index**. M5-T3 adds the
`context_swap(target_chunk_id)` tool and the active/dormant flush cycle.
Together they are the bottom half of the MCE; T4 (KV monitoring), T5
(assembly priority) and T6 (compaction) build on this store.

This ADR locks the storage, identity, API, and containment decisions
before code grows, so M5-T2's tests have a fixed target and M5-T3's route
has a fixed shape.

## Decision

### 1. Division strategy — hybrid (ADR-0020, accepted)

Code languages use tree-sitter; markdown/plain text use structural-header
regex (§10.1's own prescription for text); unsupported languages and any
parse failure use fixed-line blocks. The three strategies share one
contract: chunks tile the source byte-exactly and `Reassemble` is
byte-identical (P1–P5). **The byte-exact guarantee is the tiling
contract, not tree-sitter** — tree-sitter buys boundary *quality* and
*one model across languages*, not correctness.

### 2. Packages

- `internal/mce/division` — pure, dependency-light divider: strategies,
  `Chunk`/`Kind`, `LangFor`, `Reassemble`, boundary normalizer, the
  grammar registry and parser pool. Imports only the standard library
  plus tree-sitter.
- `internal/mce` — `ChunkStore` (SQLite), Dormant Index queries,
  contained ingestion, the `Summarizer` seam, and (M5-T3) the `ActiveSet`
  / `Swap`.

**`internal/mce` must not import `internal/llm`.** Gate G2 rule 1
forbids `internal/internalapi` from importing `internal/llm` (the pod
must have no indirect model-call path); M5-T3's `context_swap` route
lives in `internalapi` and dispatches into `internal/mce`, so an
`internal/llm` import there would subvert that rule's intent. The
summarizer is therefore an interface (§7), with the LLM-backed adapter
in `cmd/`.

### 3. Dependencies (accepted)

| Module | Version | Why | License |
|---|---|---|---|
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | cgo runtime | MIT |
| `github.com/tree-sitter/tree-sitter-go` | v0.25.0 | Go grammar | MIT |
| `github.com/tree-sitter/tree-sitter-python` | v0.25.0 | Python grammar | MIT |
| `github.com/tree-sitter/tree-sitter-javascript` | v0.25.0 | JavaScript grammar | MIT |
| `github.com/mattn/go-pointer` | (transitive) | tree-sitter binding | MIT |

Grammar Go bindings import at `.../bindings/go`. This is the project's
largest single dependency addition; CGO is already mandatory (ADR-0003),
so the build model does not change. Each grammar compiles generated C
into every binary and must be built by CI (the M5-T1 spike never touched
the main module). Adding a language is one registry entry plus its
grammar module.

### 4. Grammar registry and parser lifecycle

The spike built `tsGrammars()` and a fresh `sitter.NewParser()` **per
call**. M5-T2 changes both:

- The grammar registry (language pointers + per-language skip sets) is
  built **once**, not per call.
- Parsers are **pooled** (`sync.Pool`); a tree-sitter `Parser` is not
  safe for concurrent use, and the MCE is reachable from more than one
  goroutine. `Language` values are immutable and shared.
- Every `*sitter.Tree` is `Close()`d immediately after boundary offsets
  are extracted — trees are never retained.

A pooled parser exists only to amortize construction; correctness does
not depend on it (the spike's unpooled throughput was already
sufficient, §17 idle-path indexing).

### 5. Chunk identity — deterministic, content-derived

Chunk IDs are **deterministic** handles derived from the source hash and
byte range (hex of `sha256(source_hash || byte_start || byte_end)`,
truncated), **not** the application UUIDs of §5. Rationale:

- Re-dividing an unchanged file yields the **same** chunk IDs, so
  indexing is idempotent (§17 repository exploration) and
  `UNIQUE (source_hash, byte_start)` dedups for free.
- `context_swap(target_chunk_id)` presents the ID to the LLM; a short
  stable handle is friendlier than a random UUID.

This is a deliberate, documented deviation: §5's UUID rule governs
*entity* rows (projects, jobs, artifacts); a chunk is a content-addressed
*region of a file*, and its identity should track its content. The
`UNIQUE (source_hash, byte_start)` constraint remains the integrity
guard.

### 6. Storage — SQLite BLOB, two tables (migration 0009)

Content is stored as a `BLOB` in the existing SQLite database (§10.1
sanctions "fast local storage (SQLite/RAM)"), not as loose files. The
daemon already holds a single-connection WAL database (ADR-0003/0004);
adding a second store would fragment the state model. Two migrations:

**0009 (M5-T2):**

```sql
CREATE TABLE context_chunks (
    id             TEXT PRIMARY KEY,          -- deterministic (§5)
    source_relpath TEXT NOT NULL,             -- workspace-relative
    source_hash    TEXT NOT NULL,             -- sha256 of the whole file
    lang           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('ast','structural','header','fallback')),
    byte_start     INTEGER NOT NULL,
    byte_end       INTEGER NOT NULL,
    line_start     INTEGER NOT NULL,
    line_end       INTEGER NOT NULL,
    content_hash   TEXT NOT NULL,
    content        BLOB NOT NULL,
    project_id     TEXT,
    job_id         TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (source_hash, byte_start)
);
CREATE INDEX idx_context_chunks_source  ON context_chunks(source_hash, byte_start);
CREATE INDEX idx_context_chunks_project ON context_chunks(project_id);

CREATE TABLE dormant_index (
    chunk_id       TEXT PRIMARY KEY REFERENCES context_chunks(id),
    summary        TEXT NOT NULL DEFAULT '',
    summary_status TEXT NOT NULL DEFAULT 'pending'
                   CHECK (summary_status IN ('pending','ready','failed')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX idx_dormant_index_status ON dormant_index(summary_status);
```

plus the project's standard `_touch_updated_at` triggers. The migration
runner already disables `PRAGMA foreign_keys` around each migration and
gates the commit on `PRAGMA foreign_key_check` (ADR-0006), so the
`dormant_index → context_chunks` FK is safe.

**0010 (M5-T3):** `context_active(scope_id TEXT PRIMARY KEY, chunk_id
TEXT, updated_at TEXT NOT NULL DEFAULT (...))` — the persisted active
chunk per working scope, so a crash mid-swap resumes (§23.6).

Size caps protect the single-connection DB: `division_max_source_bytes`
(a source larger than the cap is skipped with a `context` audit row) and
`division_max_chunk_bytes` (a semantic segment larger than the cap is
**byte-split**, so one oversized declaration cannot bloat a row; a byte
split may cut mid-rune, which is acceptable because reassembly is still
byte-exact).

### 7. Summarizer seam, prompt ownership, and temperature

The Dormant Index carries a one-line summary per chunk (§10.1), produced
by the `wide` persona (§17). Because `internal/mce` may not import
`internal/llm`:

- `internal/mce` defines `type Summarizer interface { Summarize(ctx, Chunk) (string, error) }`.
- `Enrich` fills `dormant_index.summary`; a summarizer error degrades the
  row to `summary_status='pending'` (indexing is the idle path, §17 — a
  failed summary is never fatal).
- The LLM-backed adapter lives in `cmd/athanor/mce_adapter.go` over
  `llm.Registry` + `llm.Client`; tests inject a fake.

Summaries run at **temperature 0.0** so the index is reproducible (the
§10.3 determinism posture applied to index metadata). The summarization
prompt is a Core-owned, versioned "Internal Runtime" prompt (§11.1), not
a user template.

### 8. Ingestion containment

Every file read for division goes through
`internal/airlock/paths.OpenNoFollow` (§21.3, ADR-0015) — never a bare
`os.ReadFile`. The ingestion root is `<state-dir>/workspace` (the
implementation's convention, `cmd/athanor/ingress.go`; ARCHITECTURE §4's
top-level `workspace/` diagram is the aspirational layout and the
implementation wins).

### 9. `enable_lossless_swapping` becomes effective

`context_engine.enable_lossless_swapping` (declared and defaulted since
M0, never read) gates division: when `false`, ingestion refuses with a
typed error and the T3 `Swap` is unavailable. This mirrors the
`reader_mode_default` activation pattern from M4-T6.

### 10. `context_swap` is an internal API route, Core-executed (M5-T3)

`POST /internal/v1/jobs/{id}/context_swap` `{target_chunk_id}`, behind
the auth middleware and the per-job envelope, dispatching through an
injected `ContextSwapper` interface (the ADR-0019 inversion pattern) into
`internal/mce`. Job Pods have `--network=none` and cannot touch the
Core's context, so the Core performs the swap. `context_swap` joins the
closed tool set (§25) and therefore needs the matching Gate G2 route
assertion.

**Implemented (M5-T3, `409ff34`–`8bbdb0e`):** migration 0010 +
`internal/mce/active.go` (`Swap` — byte-exact load, intact flush,
crash-resumable active pointer), `context_swap` in the closed set with the
envelope-gated route and Gate G2 route/envelope coverage, and the
`cmd/athanor` adapter that translates `mce.ErrNotFound` → 404 and a disabled
swapping flag → 501. The daemon builds the adapter at boot, so the route is
live.

**Recorded deferral:** the engine does not *call* `context_swap` during a
phase in M5-T3 — the trigger is M5-T4 (KV-cache 85%/95%) and the prompt
integration is M5-T5 (§11.2 tier 3 / section 7). T3 delivers the
mechanism and the tool surface, exactly as `git_operation` entered the
envelope in M3-T5 with its call site deferred to M3-T7.

## Consequences

**Positive**

- The MCE's lossless half becomes real: byte-exact division, a persisted
  dormant store, and a swap mechanism — the substrate T4–T8 build on.
- Deterministic chunk IDs make indexing idempotent (§17) and give the
  swap tool a stable handle.
- `internal/mce` stays LLM-free, preserving Gate G2 rule 1's intent.

**Negative / accepted**

- The largest dependency addition to date (five modules, four cgo);
  grammar version churn and compile weight are recurring costs.
- BLOB content duplicates workspace bytes inside the single-connection
  DB; size caps bound the cost but do not eliminate it.

## Caveats

- Non-Go boundary-quality numbers still rest on a handful of fixtures
  (ADR-0020); M5-T2's property tests grow the corpus as real projects
  flow through.
- Markdown header-regex false positives inside code fences remain
  accepted (ADR-0020), with a fence-aware pre-scan a contained future fix.
- No downstream measure of context efficiency (swap frequency, hit rate)
  exists yet; M5-T5 is the first place to measure it end-to-end.
- Fixtures under `internal/` must not carry a `.go` suffix: Gate G1 parses
  every `.go` file under `internal/` and fails on a parse error, so the
  deliberately-broken Go cases are generated in-test and byte-sensitive
  fixtures get `.gitattributes -text` entries.
