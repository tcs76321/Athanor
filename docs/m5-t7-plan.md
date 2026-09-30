# M5-T7 Plan — `query_memory` (hybrid FTS5 + vector retrieval)

Decisions live in [ADR-0026](adr/0026-memory-retrieval.md); this is
the execution plan. The ADR is the record, the plan is the sequence — if
reality disagrees, the plan changes first (ROADMAP §9.3).

## Acceptance criteria (ROADMAP M5-T7)

- **Combined BM25 + vector results returned with scores.**
- **Respects the sqlite-vec connection-affinity constraint from task-000.**
  Recorded deviation (ADR-0026 §2): the constraint is honored by the
  single `VectorIndex` seam that owns connection affinity; the native
  extension itself is deferred to T7b/M7 packaging.

## Commit sequence

| # | Commit | Scope |
|---|---|---|
| 1 | `M5-T7.1: ADR-0026 + plan` | `docs/adr/0026-memory-retrieval.md`, this file |
| 2 | `M5-T7.2: sqlite_fts5 build tag + FTS5 boot preflight` | Makefile, `ci.yml`, `internal/store` |
| 3 | `M5-T7.3: migration 0013 — FTS5 tables + memory_embeddings` | `migrations/`, store tests |
| 4 | `M5-T7.4: retrieval engine — BM25 + cosine + RRF fusion` | `internal/mce/query.go`, `internal/mce/embed.go` |
| 5 | `M5-T7.5: Embedder seam + llm.Embed` | `internal/llm/embed.go`, `mce.Embedder` |
| 6 | `M5-T7.6: query_memory in the closed set + route + Gate G2` | `toolenvelope`, `internalapi`, `internal/gate` |
| 7 | `M5-T7.7: cmd adapter + boot wiring + config` | `cmd/athanor`, `internal/config`, `config.example.yaml` |
| 8 | `M5-T7.8: close-out` | CHANGELOG/ROADMAP/README, demo, ADR "Implemented" note |

## Per-commit detail

### 2 — build tag + preflight
- `Makefile`: a `GO_TAGS = -tags sqlite_fts5` variable applied to
  `build`, `run`, `test`, `test-race`, `test-integration`, `bench`.
- `ci.yml`: the lint step gains `--build-tags sqlite_fts5`; `vet`/`test`
  inherit the tag through the Makefile.
- `internal/store/fts.go`: `CheckFTS5(db) error` via
  `SELECT sqlite_compileoption_used('ENABLE_FTS5')`; the error names the
  `sqlite_fts5` build tag.
- `cmd/athanor/serve.go`: call `CheckFTS5` after `store.Open`, **before**
  `store.Migrate`, so a tag-less binary fails loudly and actionably.

### 3 — migration 0013
- `compacted_memory_fts` / `dormant_index_fts`: external-content FTS5
  tables + insert/update/delete sync triggers.
- `memory_embeddings(content_hash PRIMARY KEY, model, dim, vector BLOB,
  source_relpath, project_id, job_id, created_at)`.
- Tests: migration idempotency; trigger sync (insert/update/delete).

### 4 — retrieval engine
- `internal/mce/query.go`: `Query` over FTS5 (`bm25()`) and the
  `VectorIndex`; RRF fusion (k = 60); per-hit `bm25`/`cosine` scores and
  provenance; scope isolation (empty scope ⇒ nothing).
- `internal/mce/embed.go`: `VectorIndex` seam + in-Go cosine over BLOBs +
  dimension guard.
- Tests: ranking monotonicity, empty index, scope isolation, dimension
  mismatch, deterministic ordering.

### 5 — Embedder seam
- `internal/llm/embed.go`: `Embed(ctx, model, texts)` (Ollama
  `/api/embed`).
- `mce.Embedder` interface; the LLM-backed adapter stays in `cmd/`
  (`internal/mce` never imports `internal/llm`).

### 6 — tool surface
- `toolenvelope`: `ToolQueryMemory` (7 → 8) + `isKnown` +
  `QueryMemoryRequest`/`QueryMemoryResponse`/`MemoryHit`.
- `internal/internalapi/query_memory.go`: `MemoryQuerier` interface +
  handler (auth → envelope → dispatch → typed errors).
- `internal/gate/gate_g2_test.go`: add `query_memory` to `toolNames` and
  the route list.

### 7 — wiring
- `cmd/athanor/memory_query_adapter.go` + `mce_wiring.go` + `serve.go`.
- `internal/config`: `memory_embedding_model` (default `""`),
  `memory_search_top_k` (default `8`, ≥ 1); `config.example.yaml`
  documents both.

### 8 — close-out
- `docs/demo-m5-t7.md`; CHANGELOG/ROADMAP/README; ADR-0026 "Implemented"
  note; Gate G5 note if applicable.

## Verification

- `make check` green after **every** commit.
- `make test-integration` unaffected (the tag flows through it).
- Fusion tests are the acceptance criterion's test spec: "combined
  BM25 + vector results returned with scores" is asserted directly.

## Risks

- The build tag is all-or-nothing: a tag-less build must fail at the
  preflight, not mid-migration. Commit 2's test pins this.
- External-content FTS5 triggers must cover insert/update/delete; a
  missing arm shows as index drift, caught by the sync test.
- Ollama's `/api/embed` response shape differs from `/api/chat`; the
  `cmd/` adapter is the only place that knows the wire format.
