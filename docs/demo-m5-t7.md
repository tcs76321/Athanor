# M5-T7 — `query_memory`: hybrid retrieval (Demo Script)

This is the executable proof for the M5-T7 memory-retrieval surface
([ADR-0026](adr/0026-memory-retrieval.md)). Commits: T7.1 (ADR-0026 + plan),
T7.2 (build tag + preflight), T7.3 (migration 0013), T7.4 (retrieval engine),
T7.5 (`llm.Client.Embed`), T7.6 (closed set + route + Gate G2), T7.7 (adapter +
boot wiring + config), T7.8 (this close-out).

## What M5-T7 proves

| Claim | Evidence |
|---|---|
| FTS5 is compiled in; a tag-less build fails loudly and actionably | `TestCheckFTS5` (and the negative run in §2) |
| The FTS5 index tracks its source rows across insert/update/delete | `TestFTS5CompactedMemoryStaysInSync`, `TestFTS5DormantIndexStaysInSync` |
| BM25 and vector results are fused, with scores | `TestFuseRRF`, `TestRetrieverVectorFusion` |
| Free-form query text cannot smuggle FTS5 syntax | `TestFTSQueryNeutralizesOperators` |
| Retrieval never crosses a scope | `TestRetrieverScopeIsolation`, `TestMemoryQuerier_ScopeTranslation` |
| The embedding dimension is pinned per model | `TestSQLVectorIndexDimMismatch` |
| `query_memory` is in the closed set and envelope-gated | `TestGateG2ToolEnvelopeBypassImpossible`, `TestQueryMemory_*` |
| The route is live and scoped at boot | `TestMemoryQuerier_FullTextRetrieval` |

## 1. The suite

```bash
make check          # lint + vet + test-race, all with -tags sqlite_fts5
```

## 2. The FTS5 preflight fails loudly without the tag

```bash
# Without the tag: a named, actionable error before any migration runs.
CGO_ENABLED=1 go test -run TestCheckFTS5 ./internal/store/
#   --- FAIL: TestCheckFTS5
#   store: this SQLite build has no FTS5; rebuild with `-tags sqlite_fts5` ...

# With it: green.
CGO_ENABLED=1 go test -tags sqlite_fts5 -run TestCheckFTS5 ./internal/store/
#   ok  github.com/tcs76321/athanor/internal/store
```

## 3. The retrieval engine

```bash
CGO_ENABLED=1 go test -tags sqlite_fts5 \
  -run 'TestFuseRRF|TestFTSQuery|TestRetriever|TestSQLVectorIndex|TestCosine' \
  -v ./internal/mce/
```

## 4. The route, end to end

```bash
make run
# With a per-job bearer token and a task whose allowed_tools include
# query_memory (it is per-task override only, like fetch_url):
curl -s -X POST http://127.0.0.1:7420/internal/v1/jobs/$JOB/query_memory \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"config parser","project_id":"<project-id>"}'
# {"query":"config parser","scope":"<job-id>","vector_enabled":false,
#  "hits":[{"id":"cm-...","kind":"memo","score":0.0325,"bm25_rank":1,
#           "cosine_rank":0,"content":"..."}]}
```

A task whose envelope omits `query_memory` gets **403** with the
`tool_disallowed` audit row; a daemon with no MCE gets **503**; an unscoped
request gets **400**.

## What is NOT proven here

- **Native sqlite-vec `vec0`** — deferred to T7b/M7 packaging (ADR-0026 §2).
  The `VectorIndex` seam is the swap-in point.
- **The engine calling `query_memory` during a phase** — T7 ships the tool
  surface, as T3 shipped `context_swap` before T4/T5 wired its trigger. The
  LLM decides when to query; the call site is not wired in T7.
- **Vector quality with real embeddings** — the vector half reports
  `vector_enabled: false` until `context_engine.memory_embedding_model` is set
  *and* embeddings exist in `memory_embeddings`; the producer is M5-T8
  (repository indexing).
