# ADR 0127 — Retention: 365-day event archival and configurable archive keeping

**Status:** Accepted · **Date:** 2026-11-07 · **Refs:** ADR-0027, ADR-0054;
ARCHITECTURE §23, §28; `internal/retention`, `internal/store`, `internal/backup`

## Context

The event log is append-only by trigger; artifacts, corrections, and strategy
rows are never pruned. A system built to run continuously accumulates these
forever. The known-issues ledger records retention as an open 0.0.x decision.
Deleting audit rows would break the append-only invariant; keeping everything
forever makes the live table slow and the state volume grow without bound.

## Decision

### 1. Archive first, then delete under a guard

- Events older than `events_days` (default **365**) are copied to a gzip JSONL
  archive under `state/archive/events/`, fsynced, and SHA-256 recorded in a
  `retention_archives` manifest row.
- A `retention_guard` row makes the existing `events_no_delete` trigger
  conditional. Only the retention transaction flips it; a normal `DELETE FROM
  events` still aborts. Archive + manifest insert + delete run in one
  transaction; a crash rolls back.
- `daydream_logs`, closed `budget_windows`/`budget_grants`, and resolved
  `alarms` use the same 365-day window.

### 2. Keep the load-bearing records

Accepted artifacts, corrections, quarantined files, strategy outcomes, jobs,
tasks, and projects are **not** pruned by default. A future archival design for
those is separate work.

### 3. Archive keeping is an operator choice

`retention.archive_keep_days` (0 = forever, the default) and
`retention.archive_max_bytes` (0 = unlimited) are both supported. Either or
both may be set; oldest archives are pruned first, manifests verified, and the
prune audited. The UI offers “forever” (default) and “5 years” as a recommended
preset.

### 4. Maintenance is quiet

Retention runs daily at `retention.run_at` (default 03:30), in batches, and
does not `VACUUM` by default. A maintenance vacuum is explicit and only when
free disk allows it. Backups include `state/archive/` or verify its presence.
A retention failure raises an alarm.

## Consequences

- The live event table stays bounded while the audit record survives on disk.
- The append-only claim becomes precise: append-only except the guarded,
  audited retention archive path.
- Restores must account for archive files outside the SQLite backup.
- 365 days is the starting policy; the operator can keep archives forever or
  bound them by age or size.
