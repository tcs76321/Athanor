# ADR 0035 — HITL request queue (M6-T4)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §8.1,
§18.4, §20; ROADMAP M6-T4; ADR-0001 (phased state machine), ADR-0033
(dependency scheduler), ADR-0034 (failure policy).

## Context

ARCHITECTURE §20 makes HITL a live queue: a request has a type, severity,
expiry, and approve/reject/modify/defer actions, and an approval resumes the
paused job. The `hitl_requests` table has existed since migration 0002, but
it has no Go repository and nothing writes to it. §8.1's `awaiting_approval`
state is in the schema yet unreachable: `internal/job/state.go` has no edges
into or out of it.

M6-T4 has to make the state reachable, decide what a job resumes to, and give
the M6-T3 `Escalator` seam something real to call — without yet building the
first production approval trigger (that is M6-T5's HITL-gated `git_push`).

## Decision

### 1. `awaiting_from` is resumable state, mirroring `paused_from`

Migration 0018 adds `jobs.awaiting_from` with a plain
`ALTER TABLE jobs ADD COLUMN` (no table CHECK — the migration 0006
precedent). `job.Repository.Transition` records it when entering
`awaiting_approval` and refuses any resume other than the recorded state
(cancellation and failure stay legal, as with a paused job). The engine's
`Run` treats `awaiting_approval` like `paused`: it stops and waits, so
recovery (`Recover` → `Enqueue`) does not error on a job parked for a
decision.

### 2. `internal/hitl` owns the queue

A `Repo` over `hitl_requests` provides `Create`, `Get`, `Pending`, `Decide`,
and `ExpireOverdue`; a `Service` adds the job interaction:

- `Await(ctx, job, type, severity, summary, details, ttl)` moves the job to
  `awaiting_approval` (recording `awaiting_from`) and creates a pending
  request. It returns the request.
- `Decide(ctx, id, action, note, deferFor)` applies **approve** (resume to
  `awaiting_from` and re-enqueue), **reject** (fail the job), or **defer**
  (keep pending, extend `expires_at`). An unknown action is an error.
- `Expire(ctx)` denies every overdue pending request: status `expired`, and
  its job failed. Expiry therefore **denies by default**, never silently
  approves.

Every transition and decision appends a `jobs` event (`hitl_request`,
`hitl_decision`), so the audit trail is complete.

### 3. Defer is an action, not a new status

§20.1 lists defer among the actions. Rather than rebuild `hitl_requests` to
add a `deferred` status, defer keeps the request `pending` and moves
`expires_at` forward. The decision is still logged, so it is observable
without a schema change. A terminal status is only reached by approve,
reject, or expiry.

### 4. The scheduler's `Escalator` seam gets a real adapter

`cmd/athanor` wires the M6-T3 `scheduler.Escalator` to a `hitl` adapter that
creates a `task_escalation` request (high severity) for a blocked task. This
is the first caller of the queue and closes the loop T3 left open; the first
*job-linked* caller is M6-T5's `git_push`.

### 5. Expiry runs on a configured interval

A `hitl` config block sets `default_ttl` (default `24h`) and
`expiry_interval` (default `1m`). A daemon goroutine calls `Service.Expire`
on that interval and stops on shutdown; tests call `Expire` directly.

## Consequences

- `awaiting_approval` is now a first-class, resumable wait state with a
  repository-enforced origin, so a crash mid-wait resumes safely and a
  decision resumes the exact prior phase.
- An operator can list pending requests and approve/reject/defer them over
  HTTP or the CLI; with no operator, requests expire and their jobs fail,
  which is the safe default.
- M6-T5 (`git_push`) and later external-action work get one approval path;
  they call `Service.Await` rather than inventing their own.
- The queue is local and has no new direct dependency; Gate G1 is unaffected.
