# ADR 0026 — Memory retrieval: `query_memory` (hybrid FTS5 + vector), M5-T7

**Status:** Accepted · **Date:** 2026-09-30 · **Refs:** ARCHITECTURE §10.2,
§10.3, §18.3, §23, §25; ROADMAP M5-T7; ADR-0003 (SQLite/CGO single
connection), ADR-0019 (dependency inversion), ADR-0021 (chunk store,
Dormant Index, swap), ADR-0025 (compaction); task-000 findings
[docs/sqlite-setup.md](../sqlite-setup.md); F1 (`integration` CI job).

## Context

M5-T2–T6 built the MCE's storage and both treatment halves: byte-exact
division + the chunk store + the Dormant Index (T2), `context_swap` (T3),
KV-cache monitoring (T4), §10.5 assembly priority (T5), and Temp 0.0
compaction into `compacted_memory` (T6). What is missing is **retrieval**:
§25's `query_memory(query)` — *"Hybrid vector + FTS retrieval from
SQLite"* — and §10.2's third axis, **Semantic Relevance**
(`direct`/`tangential`/`dormant`, *"calculated dynamically per task via
vector similarity"*). Without it, memory can be swapped by ID but never
*found*: the Dormant Index is a table of contents, not a search index.

Two facts shape the design.

1. **FTS5 is not compiled into the current binary.** `mattn/go-sqlite3`
   exposes FTS5 only under the `sqlite_fts5` build tag; the Makefile and
   CI set no tags today. task-000 recorded this (`docs/sqlite-setup.md`)
   and it has been latent ever since. A migration that creates an FTS5
   table would fail at boot on every current build.
2. **sqlite-vec is a native extension binary, not a Go module.** The
   `.dylib`/`.so` is untracked (gitignored) and exists only in
   `spikes/sqlite-vec/build/`. Shipping it means a distribution
   mechanism, a per-platform matrix (macOS arm64/amd64, Linux
   amd64/arm64), and macOS Gatekeeper quarantine handling — a packaging
   concern the ROADMAP assigns to M7. There is also **no embedding
   producer**: `internal/llm` has only `Chat`/`Ping`, and no embeddings
   are stored anywhere.

So "hybrid FTS5 + sqlite-vec" as written cannot land green in CI today
without (a) adopting a build-wide tag and (b) shipping native binaries.
This ADR makes both decisions explicit rather than discovering them
mid-implementation.

## Decision

### 1. FTS5 is adopted project-wide via the `sqlite_fts5` build tag

The tag is threaded through every build and test invocation
(`build`, `run`, `test`, `test-race`, `test-integration`, `bench`) and
CI's golangci-lint step (`--build-tags sqlite_fts5`). Because a tag-less
binary would otherwise fail with a cryptic migration error, a boot
preflight — `store.CheckFTS5(db)`, implemented with
`SELECT sqlite_compileoption_used('ENABLE_FTS5')` — runs **before**
`store.Migrate` and returns a typed error that names the tag.

Rejected alternatives: **FTS4** (no `bm25()`; the acceptance criterion
names BM25); a **hand-written Go inverted index** (more code, worse
ranking, re-implements SQLite's tokenizer for no gain).

### 2. The vector half is a seam; native sqlite-vec is deferred

`internal/mce` gains a `VectorIndex` seam with an **in-Go cosine**
implementation over embeddings stored as BLOBs in `memory_embeddings`.
The sqlite-vec `vec0` accelerator becomes an alternative implementation
of the same seam — the documented swap-in point.

This keeps `make check` green, adds no Go dependency and no native
binary, and satisfies the acceptance criterion's *intent* (combined
BM25 + vector results returned with scores).

**Recorded deviation.** The ROADMAP row says *"respects the sqlite-vec
connection-affinity constraint from task-000"*. T7 honors the
constraint's *design* — one `VectorIndex` implementation owns the
connection-affinity question, so a future `vec0` implementation has
exactly one place to load the extension and pin it to the single pooled
connection (ADR-0003) — but it does not load the extension. The native
path is T7b/M7 packaging work. Per ROADMAP §9.3 ("when reality disagrees
with the plan, believe reality"), the plan changes and this ADR is the
record.

### 3. Embeddings are an injected seam; `internal/mce` stays LLM-free

`llm.Client.Embed` (Ollama `/api/embed`); `mce.Embedder` is the
interface; the adapter lives in `cmd/` — the same inversion as
`Summarizer` (ADR-0021 §7) and `Compactor` (ADR-0025 §5), because
`internal/mce` must not import `internal/llm` (Gate G2 rule 1's intent).
The embedding dimension is pinned per index row; a mismatch is a typed
error, never a silent truncation. `context_engine.memory_embedding_model`
defaults to empty, which makes the vector half **inert** and retrieval
FTS-only — the `search_web`-inert pattern.

### 4. FTS5 indexes the text projections, not raw chunk bytes

Two external-content FTS5 tables: one over `compacted_memory.content`
(the memos §10.3 produced) and one over `dormant_index.summary` +
`source_relpath` (the Dormant Index entries). Chunk *bodies* are BLOBs of
source code; BM25 over code is noise. Chunk content is reached through
`context_swap`; search finds the **entry**, and the hit names the chunk
ID so the model can swap it in.

### 5. Fusion is reciprocal-rank fusion; scope isolation is mandatory

Two ranked lists (BM25, cosine) fuse with **reciprocal rank fusion**
(`score = Σ 1/(k + rank)`, k = 60). RRF needs no score normalization —
which matters because BM25 and cosine are not on comparable scales — and
it degrades gracefully when one list is empty (the FTS-only default).
Each hit carries its per-signal scores (`bm25`, `cosine`) and its
provenance (`memo` vs `chunk`) so the caller can reason about *why* it
matched.

Retrieval is **scoped** by project and/or job. An empty scope returns
nothing rather than every row: publishing another scope's memory into a
prompt would be a containment leak — the same reasoning as
`ChunkStore.IndexForJob` (ADR-0023).

### 6. `query_memory` joins the §25 closed set (7 → 8), Core-executed

`ToolQueryMemory` is added to `toolenvelope`; the route is
`POST /internal/v1/jobs/{id}/query_memory`, behind the auth middleware
and the per-job envelope, dispatching through an injected `MemoryQuerier`
(the ADR-0019 inversion pattern — `internalapi` does not import
`internal/mce`). It is **per-task override only**: `job_pod.default_tools`
does not include it, so memory search is an explicit task decision,
exactly like `fetch_url` and `context_swap`. Gate G2 gains the
route-existence and envelope-bypass arms.

### 7. Config

Two new `context_engine` fields:

| Field | Default | Meaning |
|---|---|---|
| `memory_embedding_model` | `""` | Ollama embedding model. Empty ⇒ vector half inert, FTS-only. |
| `memory_search_top_k` | `8` | Max hits returned per query (≥ 1). |

### 8. Storage: migration 0013

- `compacted_memory_fts` and `dormant_index_fts`: external-content FTS5
  tables with insert/update/delete sync triggers, so the index cannot
  drift from the source rows.
- `memory_embeddings(content_hash PRIMARY KEY, model, dim, vector BLOB,
  source_relpath, project_id, job_id, created_at)`: the vector store the
  in-Go cosine implementation reads. Keyed by content hash so an
  unchanged input is never re-embedded.

## Consequences

**Positive**

- §25's `query_memory` and §10.2's Semantic Relevance axis become real:
  memory is findable, not just swappable.
- The FTS5 build-tag decision is made once, loudly, with a preflight that
  names the fix — instead of a mysterious migration failure.
- No new Go dependency and no native binary; `make check` stays green and
  CI stays portable. The `vec0` accelerator has exactly one seam to
  replace later.

**Negative / accepted**

- Every build and test invocation now carries `-tags sqlite_fts5`; a
  contributor who builds without it fails at boot with a named error.
- In-Go cosine is O(n) per query over the embedding set. Fine at local
  scale — the reason `vec0` is the deferred accelerator rather than a
  hypothetical.
- The embedding dimension is fixed per model, so switching
  `memory_embedding_model` requires a re-index. Surfaced as a typed
  error, never silent corruption.

## Deferrals

- **sqlite-vec `vec0`** — T7b or M7 packaging. The `VectorIndex` seam is
  the swap-in point; the connection-affinity work (load once on the
  single pooled connection, release immediately) belongs there.
- **The engine's `query_memory` call site** — T7 ships the tool surface,
  as T3 shipped `context_swap` before T4/T5 wired its trigger. The LLM
  decides when to query; the §11.2 §2 tool manifest is updated honestly.
- **Reading compacted memos into §11.2 §9 / tier 5** — M6 (ADR-0023 §2).

## Caveats

- FTS5 tokenization of short summaries is best-effort; the BM25 half is a
  *candidate generator*, not a relevance oracle. RRF fusion with the
  vector half is what makes ranking usable once embeddings are on.
- `memory_embeddings` grows with distinct content hashes; retention is
  future work (the same posture as `compacted_memory`, ADR-0025).
- `docs/m5-t7-plan.md` is the execution plan; this ADR is the decision
  record. If reality disagrees, the plan changes first and this ADR gains
  an "Implemented" note (the ADR-0021/0022/0023/0025 pattern).

**Implemented (M5-T7.2–T7.7):** the `sqlite_fts5` build tag is threaded
through every Makefile target and CI's lint step, with `store.CheckFTS5`
gating boot; migration 0013 adds `compacted_memory_fts`, `dormant_index_fts`
(external content + insert/update/delete sync triggers) and
`memory_embeddings`; `internal/mce/embed.go` and `query.go` hold the
`VectorIndex` seam, the in-Go cosine, and the RRF fusion; `llm.Client.Embed`
speaks Ollama `/api/embed`; `query_memory` is in the closed set with the
Core-executed route; and `cmd/athanor/memory_query_adapter.go` wires the
retriever at boot. One refinement over §4: an external-content FTS5 table
indexes exactly one source table, so `dormant_index_fts` covers `summary` and
the source path is returned with each hit by joining `context_chunks` (rather
than being indexed alongside the summary). One refinement over §6: Gate G2's
envelope-bypass check now targets *handler declarations* instead of the first
file mentioning a tool name, because the latter is
alphabetical-order-dependent and `handlers.go` (which registers every route)
sorts before `query_memory.go`.

**Updated (M5-T8):** the missing producer now exists. The repository indexing
pipeline ([ADR-0028](0028-repository-indexing.md)) walks a project's
`repository_path`, divides and stores each file, refreshes the Dormant Index
summaries, and embeds a bounded digest per summarized chunk — so the vector
half is populated in production once `context_engine.memory_embedding_model`
is set, and the FTS half is populated even without it. The "engine's
`query_memory` call site" deferral stands (the LLM decides when to query), and
native sqlite-vec `vec0` remains deferred to M7 packaging.

