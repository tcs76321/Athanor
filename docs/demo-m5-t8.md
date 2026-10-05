# M5-T8 — Repository indexing pipeline (Demo Script)

This is the executable proof for the M5-T8 producer: the pipeline that fills
the MCE's chunk store, Dormant Index, and vector store in production
([ADR-0028](adr/0028-repository-indexing.md)). Commits: T8.1 (ADR-0028 +
plan), T8.2 (migration 0014), T8.3 (project `repository_path`), T8.4 (index
manifest), T8.5 (repository walker), T8.6 (indexer + prune), T8.7 (config),
T8.8 (CLI + API route + wiring), T8.9 (daydream exploration), T8.10 (this
close-out).

## What M5-T8 proves

| Claim | Evidence |
|---|---|
| The migration adds `projects.repository_path` and `indexed_sources` with the right constraints | `TestRepositoryIndexingMigration` |
| The project repository path round-trips through create/get/set | `TestCreatePersistsProjectGoalTask`, `TestSetRepositoryPath` |
| The walker skips VCS/vendor/build directories, symlinks, binaries, and unknown languages | `TestDiscoverFilters`, `TestDiscoverNoIndexableFiles` |
| Discovery is deterministic | `TestDiscoverIsDeterministic` |
| A file's previous revision is pruned when its content changes | `TestIndexEditReindexesAndPrunesOldSource` |
| A deleted file is pruned; identical files share chunks safely | `TestIndexDeletePrunes`, `TestIndexPruneKeepsSharedChunks` |
| An unchanged repository makes no model call | `TestIndexSecondPassSkipsUnchanged` |
| A pass is bounded by files, and `RunAll` converges | `TestIndexBatchesAndCatchesUp` |
| The vector half is inert without an embedding model | `TestIndexVectorInertWithoutEmbedder` |
| A summarizer failure is recorded and retried | `TestIndexSummarizerFailureIsRetried` |
| A mid-size repo indexes end-to-end, re-passes cheaply, and its chunks are reachable via `query_memory` → `context_swap` | `TestIndexRepositoryEndToEnd` |
| The API route reports counts; 503/404/409 are correct | `TestIndexRoute*` |
| The idle driver indexes one bounded pass per project under the §17 gates | `TestDaydreamExploresRepositories`, `TestDaydreamSkipsExplorationWithoutIndexer` |

## 1. The suite

```bash
make check          # lint + vet + test-race, all with -tags sqlite_fts5
```

## 2. Migration + walker + pipeline

```bash
CGO_ENABLED=1 go test -tags sqlite_fts5 \
  -run 'TestRepositoryIndexingMigration|TestDiscover|TestIndex|TestManifest' \
  -v ./internal/store/ ./internal/mce/
```

## 3. The CLI, end to end

`project create -repo` records the repository, and `athanor index` runs the
pipeline to completion against a running daemon:

```bash
make build
make run   # in another terminal

./bin/athanor project create -name demo -archetype code \
  -goal "Index this repository completely." -repo /path/to/repo

./bin/athanor index -project <project-id>
# indexed 40, skipped 0, pruned 0, failed 0 (40 chunks, 40 summaries, 0 embeddings)

./bin/athanor index -project <project-id>
# indexed 0, skipped 40, pruned 0, failed 0 (0 chunks, 0 summaries, 0 embeddings)
```

The second run makes **zero** model calls: the manifest proves each file is
unchanged. Embeddings stay at 0 until `context_engine.memory_embedding_model`
is set; with it set, the daemon embeds a bounded digest per summarized chunk
and `query_memory` gains its vector half.

If the daemon has no indexer wired, the route answers **503**; an unknown
project **404**; a project with no `repository_path` and no `path` override
**409**. An explicit `-path` overrides the stored repository.

## 4. The route

```bash
curl -s -X POST http://127.0.0.1:7420/projects/$PROJECT/index \
  -H 'Content-Type: application/json' -d '{"path":"/path/to/repo"}'
# {"discovered":40,"indexed":40,"skipped":0,"pruned":0,"chunks":40,
#  "summarized":40,"embedded":0,"failed":0}
```

## 5. The idle action

When the daemon is idle, daydreaming is allowed, and the kill switch is not
frozen, the §17.1 Repository Exploration action runs one bounded pass per
project with a `repository_path` and audits a `repository_exploration` event.
It yields immediately when real work is queued (like memory consolidation).

## What is NOT proven here

- **Native sqlite-vec `vec0`** — still deferred to M7 packaging (ADR-0026 §2).
  `memory_embeddings` is filled by this pipeline; the `VectorIndex` seam
  remains the swap-in point for the accelerator.
- **Real embeddings** — the vector half stays inert until
  `context_engine.memory_embedding_model` names an Ollama embedding model.
  Every test here uses a fake embedder; a real-Ollama run is an operator
  exercise (not in CI).
- **Feeding project-scoped chunks into §11.2 §10 assembly** — the Dormant
  Index is job-scoped; broadening it is M6 (ADR-0028 §7). The model reaches a
  repository chunk today via `query_memory` → `context_swap`.
- **The other four §17.1 daydream actions and the `DaydreamLog`** — M7-T2.
- **Language coverage beyond Go/Python/JavaScript/markdown/text** — the
  divider's supported set; more grammars are follow-up work.
