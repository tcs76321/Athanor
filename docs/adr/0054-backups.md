# ADR 0054 — Scheduled backups and restore

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §23.4,
§23.5, §28.1 (`backup` events); ROADMAP M7-T4

## Context

`store.Backup` (VACUUM INTO) existed but ran only before a migration. §23.4
requires scheduled local backups, retention, and a tested restore.

## Decision

`internal/backup` provides four pieces:

- **Snapshot / Prune / Restore.** `Snapshot` wraps `store.Backup`;
  `Prune` keeps the newest N `athanor-v*.db` files (lexical sort is
  chronological because names embed a UTC timestamp); `Restore` copies a
  snapshot over the live database atomically (temp + rename) and removes the
  stale `-wal`/`-shm` files so SQLite cannot replay a pre-restore journal.
- **Schedule.** A dependency-free 5-field cron matcher (`*`, `N`, `a-b`,
  `*/n`, lists) — the project stays lean; the default `0 3 * * *` needs only
  a tiny subset.
- **Scheduler.** A 30-second poll runs the snapshot when the cron matches,
  once per minute, then prunes and audits a `backup_created` event.
- **CLI.** `athanor backup [-keep N]` forces one snapshot; `athanor restore
  -from <file> -force` restores offline. Both operate on the state directory
  directly, so the daemon must be stopped (it holds the database open).

`store.Backup` gained a uniqueness guard: two snapshots in the same second
would otherwise collide on the timestamped filename and `VACUUM INTO` would
fail.

## Consequences

- Only the SQLite database is snapshotted. `include_workspace_metadata` is
  accepted and recorded, but config/prompt/workspace metadata backup is
  deferred (the database already holds prompt templates and feedback; config
  is a user-owned file). This scope limit is explicit.
- The restore drill is documented in `DEVELOPMENT.md` and tested end to end
  (`TestSnapshotPruneRestore`): a post-snapshot change does not survive a
  restore, and pre-snapshot state does.
