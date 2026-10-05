# ADR 0028 — Repository indexing pipeline (M5-T8)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §10.1,
§10.2, §17.1, §25; ROADMAP M5-T8; ADR-0003 (SQLite/CGO single connection),
ADR-0019 (dependency inversion), ADR-0021 (chunk store, Dormant Index, swap),
ADR-0022 (KV-cache monitoring), ADR-0023 (context assembly), ADR-0025
(compaction), ADR-0026 (memory retrieval).

## Context

M5-T2–T7 built the MCE's storage and both treatment halves: byte-exact
division + the chunk store + the Dormant Index (T2), `context_swap` (T3),
KV-cache monitoring (T4), §10.5 assembly priority (T5), Temp 0.0 compaction
into `compacted_memory` (T6), and hybrid FTS5 + vector retrieval (T7).

What is missing is the **producer**. In production nothing writes
`context_chunks`, `dormant_index`, or `memory_embeddings`: the MCE is
constructed at boot (`cmd/athanor/mce_wiring.go`) and the `query_memory`
route is live (`ADR-0026 §6`), but the tables are empty because no code path
ever divides a repository or embeds a chunk. §17.1 names the missing action —
**Repository Exploration** (`wide` persona: *"Read repository files not yet
indexed. Update vector embeddings… Generate 1-line summaries for Dormant
Index entries."*). T8 is that action: the pipeline that reads a repository,
divides it losslessly, summarizes each chunk, and embeds it so retrieval
returns real memory.

Three facts shape the design.

1. **Division already exists and is deterministic.** `internal/mce/division`
   splits Go/Python/JavaScript via tree-sitter, markdown/plain text via
   structural headers, and everything else via fixed-line fallback. Chunk IDs
   are content-derived (`sha256(source_hash|start-end)`), so re-ingesting an
   unchanged source is a no-op. `ChunkStore.IngestFile` already routes file
   opens through the §21.3 path-containment library and skips oversize
   sources.
2. **Retrieval needs a consumer for its scope rules.** `query_memory` and the
   vector index are scoped by project and/or job (ADR-0026 §5). A repository
   belongs to a project, so indexed chunks must be **project-scoped**
   (`job_id` NULL), or retrieval would either leak across projects or return
   nothing.
3. **A naive pipeline is unbounded.** A mid-size repository is thousands of
   chunks; one summary model call per chunk plus one embedding per chunk is a
   multi-hour, multi-thousand-call operation. Indexing must be incremental
   (skip unchanged files without reading them) and bounded per pass (a batch
   of files and a budget of model calls), resumable across passes.

## Decision

### 1. The repository source is the project's `repository_path`

`projects` gains a nullable `repository_path` column. It is set at project
creation (`athanor project create -repo <path>`) and may be overridden per
invocation (`athanor index -project <id> -path <dir>`). Indexing is always
project-scoped: every chunk and embedding carries `project_id` and a NULL
`job_id`, so `query_memory` with a project scope reaches it and a job's
`context_swap` can load a hit by ID.

Rejected alternatives: a global `context_engine.index_roots` list (loses
project attribution and scope isolation); indexing an implicit
`<state>/workspace/projects/<id>` directory (nothing writes there yet, and it
hides the operator's real repository path).

### 2. Incremental state is an explicit manifest

A new table `indexed_sources(project_id, relpath, lang, size, mtime_unix,
source_hash, chunks, status, error, indexed_at, updated_at)` with
`PRIMARY KEY (project_id, relpath)` records what has been indexed. On each
pass:

- a discovered file whose `size` and `mtime_unix` match its row is **skipped
  without being read**;
- a file whose size/mtime differ is read and re-ingested; if its
  `source_hash` changed, the superseded source's rows are pruned (§5);
- a manifest row whose `relpath` was not discovered is pruned and removed.

`index_batch_files` bounds how many files a single pass processes, so a large
repository converges across passes and the CLI loops to completion.

Rejected alternatives: deriving "already indexed" from `context_chunks` alone
(requires reading every file to compute its hash — a full rescan); trusting
`mtime` alone (misses same-mtime content edits — the pipeline still hashes
whenever size/mtime differ, and a `-force` mode re-hashes everything).

### 3. The pipeline is discover → filter → divide+store → prune → summarize → embed

The stages reuse the existing seams rather than adding new machinery:

- **discover** — `filepath.WalkDir` over the repository root. Symlinks are not
  followed; a built-in denylist (`.git`, `node_modules`, `vendor`, `target`,
  `dist`, `build`, virtualenvs, caches) plus an operator-supplied
  `index_ignore_dirs` are skipped.
- **filter** — only files whose extension maps to a `division.LangFor`
  language (`go`, `python`, `javascript`, `markdown`, `text`) are indexed;
  binary files (NUL byte in the first 512 bytes) and files over
  `division_max_source_bytes` are skipped. The set is honest: those are the
  languages the divider parses and the summarizer describes well.
- **divide+store** — `ChunkStore.IngestFile`, unchanged (containment, size
  caps, deterministic IDs).
- **prune** — §5.
- **summarize** — `ChunkStore.Enrich(sourceHash, Summarizer)` with the `wide`
  persona at temperature 0.0 (already implemented in T2; idempotent — a
  `ready` summary is skipped).
- **embed** — an embedding for each chunk with a ready summary, batched
  through `Embedder.Embed`, written through `VectorIndex.Put`.

`index_batch_chunks` bounds summary + embedding model calls per pass; the
vector half is inert when `memory_embedding_model` is empty (division and
summaries still run, retrieval is FTS-only — the `search_web`-inert pattern).

### 4. The embedded text is a bounded deterministic digest, not the raw chunk

`embeddingText = relpath + " L<line_start>-<line_end> " + summary + "\n" +
first index_embed_bytes bytes of content`. The FTS half already indexes the
summary (ADR-0026 §4), so the vector half should carry *code* signal while
staying inside the embedding model's context window and remaining
deterministic. The `content_hash` recorded on the embedding row is the hash of
this digest, so a changed summary or content re-embeds; an unchanged digest is
skipped. `index_embed_bytes = 0` means path + summary only.

Rejected alternatives: embedding raw chunk bodies up to
`division_max_chunk_bytes` (128 KiB — exceeds typical embedding windows and
risks silent truncation); embedding the summary alone (loses code semantics
and duplicates what BM25 already ranks).

### 5. Pruning is explicit and referentially safe

`context_active.chunk_id` references `context_chunks(id)` with no
`ON DELETE CASCADE` and `PRAGMA foreign_keys=ON`, so a prune must run before
the delete, in one transaction:

1. delete `context_active` rows that reference a to-be-pruned chunk, and audit
   the eviction (a chunk from a rewritten or deleted file is no longer valid
   context);
2. delete `memory_embeddings` whose `owner_id` is a to-be-pruned chunk;
3. delete `dormant_index` rows (the external-content FTS triggers fire);
4. delete `context_chunks` rows.

`PruneSource(sourceHash)` handles a superseded revision; `ForgetPath` handles
a deleted file. Neither deletes a `source_hash` that the current manifest
still references.

### 6. Three triggers, one engine

- `athanor index -project <id> [-path <dir>]` — synchronous CLI; loops bounded
  passes until caught up.
- `POST /projects/{id}/index` — daemon-mediated, on the loopback external API
  (the `athanor export` pattern).
- The §17.1 **Repository Exploration** daydream action, added to the existing
  power/idle/freeze-gated loop (`cmd/athanor/daydream.go`), one bounded pass
  per tick.

All three call one `mce.Indexer`. The idle path never runs the
loop-to-completion form, so indexing yields to real work exactly like the
memory-consolidation action (ADR-0025 §6).

### 7. Scope boundary: retrieval, not automatic assembly

Repository-indexed chunks are project-scoped. `query_memory` (project scope)
finds them, and the model can `context_swap` a hit into the current job. They
do **not** appear automatically in a job's §11.2 §10 Dormant Index, which is
job-scoped (`ChunkStore.IndexForJob`). Broadening the assembly to include
project-scoped chunks is **M6** (the ADR-0023 §2 deferral pattern). T8 makes
retrieval carry real content; it does not change prompt assembly.

### 8. Config

Four optional `context_engine` fields; defaults preserve today's behavior:

| Field | Default | Meaning |
|---|---|---|
| `index_batch_files` | 50 | Max files a single pass processes. |
| `index_batch_chunks` | 200 | Max summary + embedding model calls per pass. |
| `index_embed_bytes` | 2048 | Content bytes in the embedding digest (0 = path+summary only). |
| `index_ignore_dirs` | `[]` | Extra directory names skipped, additive to the built-in set. |

### 9. No new dependency; `internal/mce` stays LLM-free

Walking uses `io/fs`, `os`, and `path/filepath`; file opens go through
`airlock/paths.OpenNoFollow`; all model access stays behind the existing
`Summarizer` / `Embedder` / `VectorIndex` seams, whose adapters live in
`cmd/`. Gate G1 (no tool execution / syscall in `internal/`) and Gate G2 (no
code outside a pod; every internal route envelope-gated) are re-proven.

## Consequences

**Positive**

- `query_memory` returns real chunks and memos: semantic memory becomes
  findable, not merely swappable. §10.2's Semantic Relevance axis has data.
- Indexing is incremental and bounded: a second run over an unchanged
  repository makes **zero** model calls; a large repository converges over
  several idle passes.
- The pipeline reuses T2/T3/T7 wholesale — no new tables for chunks or
  vectors, no new dependency, no change to retrieval or assembly.

**Negative / accepted**

- One migration (0014) adds a column and a table; the schema-version guards
  in the store tests move 13 → 14.
- `mtime`-based skipping can under- or over-index on filesystems with coarse
  timestamps; a `-force` re-hash is provided, and a content change is always
  caught once size or mtime differs.
- Language coverage is limited to the five `division.LangFor` languages.
  Extending it (more grammars, more extensions) is follow-up work.
- Indexing a large repository performs many local model calls; the batch
  budget bounds one pass, and the operator controls the embedding model.

## Deferrals

- **Broadening §11.2 §10 assembly to project-scoped chunks** — M6.
- **Native sqlite-vec `vec0`** — the `VectorIndex` seam remains the swap-in
  point for the accelerator (ADR-0026 §2, ADR-0027 §"Future").
- **Embedding retention / re-indexing on model switch** — the dimension guard
  already fails loudly; pruning old-model rows is future work.
- **Full Daydream engine, §17.3 `DaydreamLog`, OS AC/battery watcher, and the
  other four §17.1 actions** — M7-T2.
- **The `read_file` / `write_file` / `list_files` / `search_files` tools** —
  the agent reads repositories through indexing and `query_memory` today;
  direct file tools remain deferred.

## Caveats

- `docs/m5-t8-plan.md` is the execution plan; this ADR is the decision record.
  If reality disagrees, the plan changes first and this ADR gains an
  "Implemented" note (the ADR-0021/0022/0023/0025/0026 pattern).
- The embedding half is only as good as `memory_embedding_model`; leaving it
  empty is supported and leaves retrieval full-text.
