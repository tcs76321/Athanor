# M5-T2 / M5-T3 Plan — Division engine, chunk store, Dormant Index, `context_swap`

**ADR:** [0021-mce-chunk-store.md](adr/0021-mce-chunk-store.md) (division
strategy: [0020](adr/0020-division-strategy.md), accepted 2026-09-27) ·
**Acceptance (ROADMAP §M5-T2):** "Reassembly of any divided file is
byte-identical (property test); index queryable" (§10.1). **M5-T3:**
"Swap loads requested chunk byte-for-byte and flushes prior active chunk
intact; unit + integration tests" (§25). Gate G5's byte-for-byte-swap arm
is closed by T3.

## Commit sequence

Each commit: stage → `make check` → staged diff → one-line `M5-T2.x:` /
`M5-T3.x:` message → human signs.

### T2.1 — ADR-0021 + this plan; accept ADR-0020 (docs)

Lock the decisions (hybrid strategy, package layout, dependencies, parser
lifecycle, deterministic chunk IDs, SQLite BLOB schema, summarizer seam,
containment root, `enable_lossless_swapping`, `context_swap` route) before
any code. ADR-0020 flips Proposed → Accepted.

### T2.2 — tree-sitter deps + `internal/mce/division` skeleton

- `go.mod`/`go.sum`: `github.com/tree-sitter/go-tree-sitter v0.25.0`,
  `tree-sitter-{go,python,javascript} v0.25.0`,
  `github.com/mattn/go-pointer` (transitive). `go mod tidy`.
- `internal/mce/division/`: `doc.go`; `Chunk`, `Kind`
  (`ast|structural|header|fallback`), `Strategy`; `Reassemble`;
  boundary normalizer + `buildChunks`; header splitter; fallback splitter
  (`division_fallback_lines`); `LangFor` (`.go`, `.py`, `.js`/`.mjs`/`.cjs`,
  `.md`/`.markdown`, `.txt`, else `""`).
- Grammar registry built **once**; parser pool (`sync.Pool`); every
  `*sitter.Tree` closed after extracting offsets.
- Prove the grammars build in the **main module** (`make build` on darwin;
  CI parity on ubuntu — the M5-T1 spike never touched the main module).
  Re-prove Gates G1/G2.

### T2.3 — tree-sitter strategy + property tests

- `internal/mce/division/strategy_treesitter.go`: per-language skip sets
  (go: package_clause/import_declaration/comment; python: import
  statements/comment; javascript: import_statement/comment), boundaries at
  named-root-child start bytes.
- **P1–P5 property tests** (the acceptance criterion): fixtures under
  `testdata/` (Python/JavaScript/Markdown/edge) + a test walking this
  repo's own `internal/**.go` (the "real repo" half). **No `.go` fixture
  files** — Gate G1 parses every `.go` under `internal/`; broken-Go cases
  are generated in-test. CRLF fixtures get `.gitattributes -text` entries.
- Boundary-agreement test (precision/recall) and broken-input P5 test.
- `internal/mce/division/bench_test.go`: keep the corpus benchmark.

### T2.4 — migration 0009 + `internal/mce` chunk store

- `migrations/0009_context_chunks.sql` (ADR-0021 §6 DDL + triggers).
- `internal/mce/store.go`: `ChunkStore` `Put`/`Get`/`ListBySource`/
  `IndexForSource`/`Reassemble`/`VerifySourceHash`; deterministic IDs;
  tx-per-ingest; `ErrNotFound`; a bitrot error mirroring
  `artifact.ContentMismatchError`.
- Headline test: **byte-exact reassembly round-trip through SQLite**.
- `migrate`/idempotency coverage.

### T2.5 — ingestion + config + audit

- `internal/mce/ingest.go`: read via `paths.OpenNoFollow` only; skip
  sources over `division_max_source_bytes` with a `context` audit row;
  split oversized AST nodes at `division_max_chunk_bytes`;
  `enable_lossless_swapping=false` → typed refusal (mirrors
  `ErrReaderDisabled`).
- Config (`context_engine`): `division_max_source_bytes` (1 MiB),
  `division_max_chunk_bytes` (128 KiB), `division_fallback_lines` (120);
  struct + `defaults.go` + `validateRaw`/`validateCross` +
  `config.example.yaml` + ARCHITECTURE §29 in the **same commit**;
  `TestExampleConfigMatchesDefaults` stays green.

### T2.6 — Summarizer seam + `wide` adapter

- `internal/mce/summarize.go`: `Summarizer` interface; `Enrich` fills
  `dormant_index.summary`; failure → `summary_status='pending'`.
- `internal/prompt`: versioned summarization prompt (Core-owned, §11.1).
- `cmd/athanor/mce_adapter.go`: adapter over `llm.Registry` +
  `llm.Client` (`wide`, temperature 0.0).
- Tests: fake summarizer; degrade-on-error; prompt determinism.

### T2.7 — daemon wiring + boot test

- Construct `ChunkStore` + summarizer adapter at boot (`serve.go`), fail
  loudly; no engine call site yet (documented; M4-T5 Gateway precedent).
- Boot test asserting construction.

### T2.8 — close-out (docs)

- CHANGELOG, ROADMAP status (M5-T2 done; Gate G5 progress), README MCE
  bullet, ADR-0021 amendment if reality disagreed.

### T3.1 — migration 0010 + `ActiveSet` + swap

- `migrations/0010_context_active.sql`.
- `internal/mce/active.go`: `ActiveSet` with persistent scope;
  `Swap(ctx, scope, targetChunkID) (flushed, loaded, error)` — loaded
  chunk byte-exact, flushed prior chunk intact and persisted.
- Tests: byte-exact load, intact flush, crash-resume of the active row.

### T3.2 — widen tool set + route + Gate G2

- `internal/toolenvelope/allowlist.go`: `ToolContextSwap` + `isKnown`;
  `allowlist_test.go` enumeration.
- `internal/gate/gate_g2_test.go`: `toolNames` + route-existence
  assertion.
- `internal/internalapi`: `POST /internal/v1/jobs/{id}/context_swap`
  (`authMiddleware`-wrapped), `ContextSwapper` interface, request/response
  types, error mapping (`403` disallowed, `400` bad input, `404` unknown
  chunk); `runner` method.
- Envelope check via `a.tools.EnvelopeFor` exactly as the other tool
  routes.

### T3.3 — cmd adapter + serve wiring

- `cmd/athanor/context_swap_adapter.go`: `ContextSwapper` over
  `internal/mce`; constructor grows (M2-T4 precedent).

### T3.4 — close-out (docs)

- CHANGELOG, ROADMAP (M5-T3 done; Gate G5 byte-exact-swap arm noted),
  README tool-set update.

## Risks / notes

- **No `.go` fixtures under `internal/`** (Gate G1 parse walk); CRLF
  fixtures need `.gitattributes -text`.
- `internal/mce` must **not** import `internal/llm` (Gate G2 rule-1
  intent); the summarizer is an interface with a `cmd/` adapter.
- `internalapi` must not import `internal/mce`'s LLM side — it imports
  only the `ContextSwapper` interface it defines, satisfied in `cmd/`.
- Grammar build on CI: ubuntu-latest has `gcc`; compile time increases —
  verify in T2.2, not assumed.
- `context` is already a configured event-log category
  (`config.Categories`); MCE audit rows use it.
- One commit per logical change; one `-m`; GPG-signed by the human.
