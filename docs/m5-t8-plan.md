# M5-T8 Plan — Repository indexing pipeline

**ADR:** [0028-repository-indexing.md](adr/0028-repository-indexing.md) ·
**Acceptance (ROADMAP §M5-T8):** "Incremental indexing of a mid-size repo
completes; new files indexed without full rescan" (§17).

**Scope:** the producer that fills `context_chunks`, `dormant_index`, and
`memory_embeddings` in production. It reuses division (T2), summaries (T2),
the vector index (T7), and swap (T3); it adds a project `repository_path`, an
incremental manifest, the pipeline, three triggers (CLI / API / daydream), and
the acceptance tests. Reading project-indexed chunks into the §11.2 §10
assembly is **M6** (ADR-0028 §7); the full Daydream engine is **M7-T2**.

**Sizing note:** the ROADMAP labels T8 `M` (2–4h); the full slice is closer to
`L` (4–8h). The commits below are individually small and reviewable; if the
effort ceiling binds, land commits 1–8 + 10–13 as **T8a** and commit 9 as
**T8b**.

## Commit sequence

Each commit: last write finished alone → `make check` green → staged diff →
one-line `M5-T8.x:` message → human signs. Re-prove **Gate G1** after 8.6 and
**Gate G2** after 8.8 with `-count=1`.

| # | Commit | Scope |
|---|---|---|
| 1 | `M5-T8.1: ADR-0028 + plan` | `docs/adr/0028-repository-indexing.md`, this file |
| 2 | `M5-T8.2: migration 0014 — repository_path + indexed_sources` | `migrations/0014_repository_indexing.sql`, store tests |
| 3 | `M5-T8.3: project repository_path plumbing` | `internal/project`, `internal/api`, `cmd/athanor/cli_project.go` |
| 4 | `M5-T8.4: index manifest store` | `internal/mce/manifest.go` + tests |
| 5 | `M5-T8.5: repository walker` | `internal/mce/index_walk.go` + tests |
| 6 | `M5-T8.6: indexer orchestration + prune` | `internal/mce/index.go`, `internal/mce/prune.go` + tests |
| 7 | `M5-T8.7: config fields + example` | `internal/config`, `config.example.yaml` |
| 8 | `M5-T8.8: CLI + API route + boot wiring` | `internal/api`, `cmd/athanor` |
| 9 | `M5-T8.9: daydream repository exploration` | `cmd/athanor/daydream.go` + test |
| 10 | `M5-T8.10: acceptance test + demo` | `cmd/athanor/mce_index_test.go`, `docs/demo-m5-t8.md` |
| 11 | `M5-T8.11: close-out` | CHANGELOG / ROADMAP / README, ADR "Implemented" note |
| 12 | `M5-T8.12: docs accuracy sweep — markdown` | living docs + ADR status notes |
| 13 | `M5-T8.13: docs accuracy sweep — stale code comments` | no behavior change |

## Per-commit detail

### 2 — migration 0014
- `ALTER TABLE projects ADD COLUMN repository_path TEXT;`
- `CREATE TABLE indexed_sources` per ADR-0028 §2 + `touch_updated_at` trigger.
- Bookkeeping: `internal/store/store_test.go` version 13 → 14 and
  `wantTables`; append `"0014"` to the `migrationsExcept(...)` lists in
  `internal/store/enum_migration_test.go` and
  `internal/store/job_migration_test.go`, and bump their want versions to 14.

### 3 — project repository_path
- `project.Project` gains `RepositoryPath string`; `Repo.Create` takes a
  `repositoryPath` argument (five call sites: the API handler and four tests).
- `internal/api`: `projectRequest` / `projectResponse` carry `repository_path`.
- `cmd/athanor/cli_project.go`: `project create -repo`.
- `project.Get` returns the field.

### 4 — manifest
- `internal/mce/manifest.go`: `SourceRow`, `IndexManifest` with
  `Get/List/Put/Delete`, `UNIQUE(project_id, relpath)` semantics.
- Tests: round-trip, upsert preserves/updates, list scoping, delete miss.

### 5 — walker
- `internal/mce/index_walk.go`: `FileRef`, `WalkOptions`, `Discover(root, opts)`.
- Built-in denylist + `index_ignore_dirs`; extension filter via
  `division.LangFor`; binary sniff; oversize skip; symlink skip.
- Tests: denylist, extension filter, binary, oversize, symlink, deterministic
  ordering.

### 6 — indexer + prune (the core)
- `internal/mce/index.go`: `IndexOptions`, `IndexResult`, `Indexer`,
  `NewIndexer`, `RunOnce`, `RunAll`.
- `internal/mce/prune.go`: `PruneSource`, `ForgetPath` (§5 ordering).
- Tests with fake `Summarizer`/`Embedder`/`VectorIndex`: first run indexes;
  unchanged run is all-skip with zero model calls; edit re-ingests + prunes;
  delete prunes; batching; vector-inert; idempotence property.
- **Gate G1 re-prove.**

### 7 — config
- `context_engine`: `index_batch_files` (50), `index_batch_chunks` (200),
  `index_embed_bytes` (2048), `index_ignore_dirs` ([]); defaults + validation.
- `config.example.yaml` documents them; `TestExampleConfigMatchesDefaults`
  stays green.

### 8 — CLI + API + wiring
- `internal/api`: `IndexRunner` interface + `SetIndexRunner`; `POST
  /projects/{id}/index` (404 / 409 / 200).
- `cmd/athanor/mce_index.go`: build the `Indexer` from `mceRuntime` +
  `llmEmbedder` (when the model is set) + `mce.NewSQLVectorIndex`.
- `cmd/athanor/cli_index.go`: `athanor index`.
- `serve.go`: wire the runner.
- **Gate G2 re-prove.**

### 9 — daydream repository exploration
- `cmd/athanor/daydream.go`: a bounded `IndexOnce` per project with a
  `repository_path`, under the existing gates; audit `daydream`
  `repository_exploration` with the `IndexResult` counters.

### 10 — acceptance test + demo
- End-to-end over a synthetic mid-size repo (40 files + ignored dirs +
  binary + oversize): first pass indexes, second pass no-ops, edit/delete
  converge, `query_memory` finds a chunk, `context_swap` loads it.
- `docs/demo-m5-t8.md`; optional real-Ollama probe documented, not in CI.

### 11–13 — close-out + documentation accuracy
- M5 ✅ in ROADMAP; README "What works / next / deferred" corrected; CHANGELOG
  entry; ADR-0028 "Implemented" note.
- The markdown accuracy sweep and the stale-code-comment sweep enumerated in
  the plan's audit tables.

## Verification

- `make check` green after **every** commit.
- `make test-integration` unaffected (no pod / internet surface changes).
- No new direct dependency; `internal/deps/deps_test.go` unchanged and green.
- `internal/mce` still imports no `internal/llm`.

## Risks

- **Model-call volume** on a real repository: bounded by `index_batch_chunks`;
  the manifest makes passes resumable; `RunAll` is CLI-only.
- **Prune vs. active chunk**: the FK makes the ordering in §5 mandatory; a
  dedicated test pins it.
- **Schema-version churn**: the test-bookkeeping checklist in commit 2 covers
  every pinned list.
- **Scope leakage**: an empty scope returns nothing (ADR-0026 §5); indexing
  always sets a project scope.
