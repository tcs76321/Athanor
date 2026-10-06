# ADR 0055 — Morning Digest

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §27.1,
§27.2; ROADMAP M7-T6

## Context

§27.2 specifies an asynchronous overnight summary, but nothing aggregated the
activity that already exists across jobs, artifacts, HITL, daydream logs, and
strategy outcomes.

## Decision

`internal/digest.Build(ctx, store, since, until)` is a read-only aggregation
returning a `Digest`:

- jobs completed/failed/cancelled, with the failed jobs' recorded error
  reasons (from their `job_failed` events);
- goals completed and tasks done;
- artifacts created by status (draft/accepted/rejected/candidate);
- pending HITL requests and active alarms;
- daydream activity (actions, artifacts/corrections/insights produced, chunks
  processed, tokens saved);
- token usage over the window.

It is exposed at `GET /digest?hours=N` (default 12) and
`athanor digest [-hours N]`. Generation is synchronous on request; §27.2's
"scheduled morning" delivery can call the same function from a scheduler
later without changing the aggregation.

## Consequences

- The digest invents nothing: every number is a SQL count over the window, so
  it is deterministic and easy to test (`TestBuildAggregatesWindow`).
- Token usage reads `strategy_outcomes.token_cost`; a window with no captured
  outcomes reports 0 rather than guessing.
- The window boundary uses `updated_at`/`created_at`/`started_at` timestamps
  in UTC RFC3339, matching how every table stores time.
