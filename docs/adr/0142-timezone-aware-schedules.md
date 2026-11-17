# ADR-0142 — Timezone-aware schedules

**Status:** accepted · **Date:** 2026-11-17 · **Refs:** [ADR-0134](0134-project-cycles-and-blocks.md)

## Context

Cycle schedules were cron fields interpreted in UTC. A daily brief at 07:00
local required the operator to convert the hour and to reconvert it at a DST
boundary. The UI displayed "UTC", so the mismatch was at least visible, but the
product promise — "a daily news report" — is a local-calendar promise.

## Decision

1. **A trigger has a timezone.** `triggers.timezone` (migration 0053) stores an
   IANA name; the default is `UTC`, so every existing trigger keeps its
   behavior.

2. **Cron fields are local; storage is UTC.** `NextRunIn(spec, now, loc)`
   interprets the schedule in the location and returns the instant in UTC.
   `next_run_at` remains RFC3339 UTC, so ordering and the at-most-once claim
   are unchanged.

3. **DST follows the local calendar.** `time.Add` in the location produces the
   next local minute; a spring-forward gap skips the missing hour and a
   fall-back repeats the hour once, which is what a calendar user expects.

4. **The UI is explicit.** The new-cycle form defaults to the server's local
   timezone and sends it with the schedule; the project page shows the
   timezone next to the cron. CLI/API callers that do not choose keep UTC.

## Consequences

- "07:00" means 07:00 where the operator lives, including across DST.
- A trigger created before this migration is byte-identical in behavior.
- A bad timezone is rejected at creation with a clear error, not silently
  treated as UTC.
