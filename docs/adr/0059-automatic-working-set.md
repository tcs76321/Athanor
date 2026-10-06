# ADR 0059 — Automatic working set: project repository chunks in the Dormant Index

**Status:** Accepted — lands with F5, supersedes [ADR-0048](0048-reduce-and-complete.md) §T7c ·
**Date:** 2026-10-06 · **Refs:** ARCHITECTURE §10.1, §10.5, §11.2 §10,
§14; [ADR-0021](0021-mce-chunk-store.md), [ADR-0026](0026-memory-retrieval.md),
[ADR-0028](0028-repository-indexing.md); ROADMAP F5

## Context

The MCE's flagship mechanism is the Dormant Index (§10.1): a table of
contents the prompt publishes so the model can `context_swap` a full-fidelity
chunk it needs. The repository-indexing pipeline (ADR-0028) ingests a
project's repository into the chunk store with a **project** attribution, and
`context_swap` already admits project-owned chunks (`ChunkStore.Owns`). But
the engine's context provider queried the Dormant Index by **job**
(`IndexForJob`, ADR-0023 §7), and no production path ingests repository chunks
with a job ID — so tiers 3 and 6 were empty for normal jobs, and repository
context was reachable only through the `query_memory` tool.

ADR-0048 (§T7c) consciously deferred the fix: injecting a whole project index
would bloat the working set, and sizing a relevance policy needed probe token
data. F5 builds the bounded, relevance-ranked injection that ADR-0048
deferred.

## Decision

**Tier 6 (the Dormant Index) now unions the job's own chunks with ranked,
bounded project-repository chunks.**

- `internal/mce` gains `ChunkStore.IndexForProject(ctx, projectID, query,
  limit)`: repository chunks are exactly `project_id = ? AND job_id IS NULL`.
  With full-text tokens in `query` it ranks by FTS5 `bm25()` over the
  `dormant_index_fts` summary index (migration 0013); with no tokens it falls
  back to most-recently-updated rows. It makes **no model call**.
- The `ContextProvider` seam is widened to a `ContextQuery{JobID, ProjectID,
  Query, Limit}`. `ActiveChunk` keeps the job scope; `DormantIndex` unions the
  job's rows (`IndexForJob`) with the ranked project rows (`IndexForProject`),
  de-duplicated by chunk ID, job rows first.
- The engine builds the query hint from the task title, description, and
  acceptance criteria (`Engine.contextQuery`) and passes the configured
  `context_engine.repository_index_limit` (default 20). It audits a
  `context`/`dormant_index_sourced` row with the row count, project, and limit.
- Because `Owns` already admits project chunks and the `context_swap` route
  attaches the job's project server-side, every published project row is
  swappable by the job with no route change.

**Tier 3 seeding is opt-in and byte-capped.** When
`context_engine.seed_active_chunk` is true (default **false**), the adapter
activates the single top-ranked project chunk into tier 3 when a job has no
active chunk, provided it fits `seed_active_max_bytes` (default 16 KiB). Tier
3 is full-fidelity and never evicted, so this is off unless an operator asks
for it.

## Consequences

- A normal job now sees its project's repository in the prompt's Dormant
  Index and can pull any entry with `context_swap`; the flagship MCE path is
  exercised by the default execution flow, not only by the tool.
- The bounds keep the promise ADR-0048 protected: tier 6 is metadata-only,
  capped at `repository_index_limit`, ranked rather than dumped, and is the
  **first** tier the §10.5 ladder evicts — so an over-large index cannot
  breach the context budget.
- Ranking and the fallback are deterministic (SQLite ordering), so prompt
  assembly stays reproducible for a fixed store.
- The integration proves the union end-to-end
  (`cmd/athanor/context_provider_test.go`) and the ranking/scope read
  (`internal/mce/index_project_test.go`). Gate G5's byte-exact arms are
  untouched.

## Alternatives rejected

- **Inject the whole project index** (ADR-0048's rejected option). Unbounded
  prompt growth; would regress the context floors.
- **Embedding-similarity ranking.** Requires a model call per prompt
  assembly and the vector half is inert without an embedding model
  (ADR-0026 §3); FTS5 bm25 over the existing summary index is free and
  deterministic.
- **Seed tier 3 by default.** Full-fidelity and never evicted; default-off
  with a byte cap is the safe posture.
- **Ingest repository chunks per job.** Duplicates storage per job and
  re-introduces the scope confusion; the union at read time is simpler.

## Not in scope

- A UI/settings surface for the limit (config-only for now).
- Relevance signals beyond summary FTS (path filters, embeddings) — a future
  enhancement; the `IndexForProject` seam is the place to add them.
