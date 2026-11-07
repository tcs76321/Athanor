# ADR 0003 — SQLite driver: mattn/go-sqlite3 with CGO and a single connection

**Status:** Amended by ADR-0125 (single writer + read-only pool) · **Date:** 2026-08-25 · **Refs:** ARCHITECTURE §23; ROADMAP M0-T4, TASK-000; `docs/sqlite-setup.md`

## Context

The store layer needs SQLite now and vector similarity later (M5). sqlite-vec ships as a loadable extension that must be loaded at runtime via `LoadExtension`. The task-000 spike (`spikes/sqlite-vec`, findings in `docs/sqlite-setup.md`) tested the two viable Go drivers:

1. `mattn/go-sqlite3` — CGO; loads `.so`/`.dylib` extensions at runtime out of the box.
2. `modernc.org/sqlite` — pure Go; **cannot** load dynamic extensions (would require a custom Wasm build of SQLite with sqlite-vec compiled in).

The spike also established that SQLite extensions are loaded **per connection**, so any vec0 usage must happen on the same connection that loaded the extension.

## Decision

Use `mattn/go-sqlite3` with CGO enabled, cap the pool at one connection (`SetMaxOpenConns(1)`), and open with WAL + `synchronous=NORMAL` + `busy_timeout` + `foreign_keys=ON`. FTS5 requires the `-tags sqlite_fts5` build tag when needed.

## Consequences

- Every build, test run, CI job, and release artifact needs a C toolchain (`CGO_ENABLED=1`). Cross-compilation and container packaging (M7) inherit this cost.
- All database access serializes through one connection. SQLite is single-writer regardless, so write serialization is inherent; the real trade-off is giving up WAL's concurrent-reader benefit until a read-pool strategy exists. See [ADR 0004](0004-db-concurrency-model.md).
- Connection affinity for sqlite-vec is trivially satisfied today (one connection); it becomes a real constraint again in M5 when vec0 tables arrive.
- If pure-Go builds ever become mandatory (e.g., a no-CGO target), FTS5-only operation on `modernc.org/sqlite` remains the documented fallback path.
