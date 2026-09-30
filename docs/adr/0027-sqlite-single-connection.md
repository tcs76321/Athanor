# ADR 0027 — SQLite single-connection discipline (F2)

**Status:** Accepted · **Date:** 2026-09-30 · **Refs:** ARCHITECTURE §23, §23.1,
§23.5, §28; ROADMAP (F2, foundational); ADR-0003 (SQLite/CGO single
connection), ADR-0004 (DB concurrency model), ADR-0026 (memory retrieval — the
first consumer that could hold a connection).

## Context

ADR-0003 pins the daemon to a single SQLite connection (`SetMaxOpenConns(1)`)
so a runtime-loaded extension (sqlite-vec, if adopted) has stable connection
affinity; ADR-0004 records the concurrency model. The consequence is easy to
state and easy to forget: **the pool has exactly one connection, so anything
that holds it also holds the entire database.**

Two ways to hold it, both invisible in review:

1. A transaction left open across I/O (a model call, an HTTP fetch, a pod
   exec). The connection is checked out for the duration, and every other
   goroutine blocks on `QueryContext`.
2. A `*sql.Rows` that is never drained or closed. Same effect.

Neither deadlocks loudly. With a 5 s `busy_timeout` over WAL, the daemon simply
stalls, and the symptom is a hung job rather than an error.

An audit of the tree (2026-09-30) found the invariant **already holds**: nine
transactions, all DB-only, and no `db.Conn()` use anywhere.

| Site | Function | Work |
|---|---|---|
| `internal/store/migrate.go:189` | `applyOne` | one migration's DDL + bookkeeping |
| `internal/store/quarantine.go:92` | `QuarantineRepo.Put` | quarantine row + audit event |
| `internal/mce/store.go:172` | `ChunkStore.PutSource` | chunks + Dormant Index rows + audit event |
| `internal/project/repo.go:32` | `Repo.Create` | project + first task |
| `internal/job/transition.go:63` | `Repository.Transition` | the state CAS |
| `internal/evaluation/repo.go:109` | `Repo.Create` | one evaluation record |
| `internal/artifact/persist.go:28` | `Store.persist` | artifact row + audit event |
| `internal/artifact/store.go:94` | `Store.NewVersion` | one version row |
| `internal/artifact/queries.go:186` | `Store.SupersedeAndAccept` | the §9.3 supersede+accept transition |

`internal/engine` — the package that owns the LLM client and the phase loop —
creates **no** transaction today. That is the property worth freezing: M6's
parallel DAG work and M5-T8's indexing pipeline are the next places a
contributor could wrap a model call in a transaction and stall the daemon.

## Decision

### 1. The contract

- One connection. Never hold a transaction, or an un-drained `*sql.Rows`,
  across I/O.
- Transactions live in the storage packages (`internal/store` and the repos
  over it: `job`, `project`, `artifact`, `evaluation`, `mce`). They do not
  appear in orchestration packages (`internal/engine`, `cmd/`).
- `db.Conn()` is reserved for extension loading on the single pooled
  connection and must be released immediately; no other caller may take it.

### 2. Enforced structurally, not by convention

A gate test (the `internal/gate` family) fails the build when `BeginTx` /
`Begin(` appears in `internal/engine` or `cmd/`, or when `db.Conn(` appears
outside `internal/store`. The allowlist is a **package**, not a file: a new
transaction in a storage package is a non-event, while one in the engine is a
build break.

### 3. Guarded at runtime

`internal/store` gains a concurrency guard test: many goroutines mixing reads
and writes plus a deliberately long read, bounded by a deadline, asserting
completion and `Stats().InUse == 0` afterwards. A leaked connection deadlocks
the pool, so the test hangs (and the race-enabled suite's timeout catches it)
rather than passing quietly. The §23.1 pragmas (`journal_mode=WAL`,
`synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`, `MaxOpenConns=1`)
are pinned by test so a refactor cannot weaken recovery silently.

## Consequences

**Positive**

- The single-connection contract becomes a build-time and test-time property
  instead of a comment in ADR-0003 that a contributor has to have read.
- The next contributor gets a named failure ("`internal/engine` must not open a
  transaction") instead of a hung daemon and a debugging session.
- The `db.Conn()` rule pre-names the one legitimate caller, so adopting
  sqlite-vec later has an obvious, gate-sanctioned home.

**Negative / accepted**

- The gate is a structural rule over identifiers, not a proof of liveness. It
  cannot see a transaction that is opened in an allowed package and handed to
  the engine.
- The runtime guard is a smoke test: it catches leaks that reproduce under the
  tested workload, not every leak.

## Caveats

- The rule constrains *holding* the connection, not transaction *count*: nine
  short transactions are fine, one long one is not. "I/O" here means network,
  model, or process calls — a slow local disk write inside a transaction is
  within the contract.
- If sqlite-vec is adopted (ADR-0026 §2, deferred to T7b/M7), the loader belongs
  in `internal/store` and must release the connection before returning. The
  `db.Conn(` rule makes that location explicit rather than accidental.
