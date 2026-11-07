# ADR 0125 — Multi-slot SQLite: single writer plus a read-only pool, and mission lanes

**Status:** Accepted · **Date:** 2026-11-07 · **Refs:** ADR-0003, ADR-0004,
ADR-0027, ADR-0072, ADR-0096; ARCHITECTURE §23, §24; `internal/store`,
`internal/scheduler`, `internal/engine`

## Context

ADR-0003 capped SQLite at one connection so a runtime-loaded extension could
keep stable connection affinity, and explicitly recorded the cost: WAL's
concurrent-reader benefit is given up until a read-pool strategy exists.
ADR-0027 hardened the single-connection discipline (no transactions across I/O,
no `db.Conn()` outside the store). Multi-slot work now needs reads to proceed
while a job writes, without introducing `SQLITE_BUSY` on the write path.

## Decision

### 1. One writer, many readers

- The writer pool is `MaxOpenConns(1)` with `_txlock=immediate`, WAL,
  `busy_timeout`, and `foreign_keys=ON`. Every write goes through it; writes
  remain serialized by design.
- A read-only pool opens the same file with `mode=ro`, `MaxOpenConns(N)`, and
  the same busy timeout. `Store.ReadDB()` exposes it.
- `Store.DB()` remains the writer for compatibility. Read-heavy paths migrate
  incrementally: event streams, digest, list handlers, benchmark collectors,
  MCE retrieval, and the UI.
- Backups continue to use `VACUUM INTO`; the implementation must prove it under
  concurrent writes and fall back to the writer if the read-only handle refuses.
- Migrations run on the writer at boot before the read pool is used.

### 2. Lanes

`capacity.Admission` owns lane-aware slots:

- **solo**: one slot at the solo plan, reserved when a mission has a task the
  swarm cannot run honestly;
- **swarm**: `N` slots at the swarm plan, for eligible tasks only.

The aggregate `MaxConcurrentJobs()` remains the engine's cap; a swarm job never
consumes the reserved solo slot. Inference capacity is acquired before the
phase budget starts, so a queued call cannot time out on a phase deadline it
never had.

### 3. Structural rules stay

Transactions remain in storage packages, never in `internal/engine` or `cmd/`.
`Conn()` remains reserved to `internal/store`. The single-connection comment
and tests are updated to the writer/read-pool shape, not deleted.

## Consequences

- Reads no longer block on a job's short write transaction, and writes do not
  contend with UI/SSE polling.
- Write throughput is unchanged (SQLite is single-writer), which is the honest
  ceiling; concurrency comes from interleaving model/pod work, not parallel
  writes.
- sqlite-vec, if adopted later, needs a dedicated connection or a per-connection
  loader; the read pool does not assume extension affinity.
- The scheduler already starts every ready leaf; lanes decide how many of those
  jobs run at once and at what context.
