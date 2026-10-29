# ADR 0087 — Cancellation is a cooperative phase-context cancel

**Status:** Accepted · **Date:** 2026-10-28 · **Refs:**
ARCHITECTURE §8.2 (state machine), §20 (watch/interruption), §23.6
(recovery), §24 (power); [ADR-0060](0060-shutdown-semantics.md)
(abandon-and-recover), [ADR-0086](0086-job-panic-containment.md) (containment).

## Context

The engine enqueues one goroutine per job with `context.Background()`, and a
phase's LLM/pod calls derive from that context. The UI's Cancel action changed
the job row directly. A running phase did not observe the row change, so a
long LLM call or test run continued until it finished and then lost the CAS
race — at best wasted work, at worst a phase that appeared to overwrite a
terminal state in logs. There was no way for an operator or a shutdown path to
stop the work itself.

## Decision

1. **Add `EnqueueContext(ctx, jobID)` and keep `Enqueue` as its background
   wrapper.** The dispatcher waits for a concurrency slot cancellably; a
   cancelled wait abandons cleanly without running the job.
2. **A per-job context is created at enqueue and registered in a cancel map.**
   `Engine.Cancel(jobID)` marks the job as user-cancelled and cancels that
   context. The mark distinguishes a user cancel from an internal deadline or
   shutdown cancel.
3. **`Run` commits `cancelled` when the phase returns `context.Canceled` and
   the job is marked.** The commit uses a detached, bounded context (the phase
   context is already canceled), writes a `job_cancelled` audit row, notifies
   the scheduler terminal seam, and stops the pod. It never writes `failed` for
   a user cancel.
4. **The UI prefers the engine path.** Its Cancel action calls `Cancel` when the
   engine exposes it, falling back to the direct transition only when no job
   goroutine owns the id (already terminal or never enqueued).
5. **Shutdown is unchanged.** The daemon still abandons in-flight phases and
   relies on committed transitions plus `Recover` (ADR-0060); it does not mark
   those jobs cancelled.

## Consequences

- Cancel actually stops the model call or pod command instead of waiting for
  it; the job ends in the canonical `cancelled` state.
- The cancel map is per running goroutine and cleaned in a `defer`, so a
  terminal job leaves no entry.
- A phase that ignores its context can still delay cancellation; the state is
  not committed until the phase returns. That is a phase bug, now observable,
  not a reason to write a terminal row under a running phase.
- The scheduler's terminal seam sees `cancelled`, which the M6-T3 failure
  policy already treats as terminal (no retry).

## Alternatives rejected

- **Write `cancelled` immediately from the UI.** Races the running phase and
  makes the audit trail lie about when work stopped.
- **Use process shutdown for cancel.** Kills the daemon and every other job.
- **Poll a cancellation flag in each phase.** More call sites, easy to miss;
  the context already threads through the LLM and pod calls.
