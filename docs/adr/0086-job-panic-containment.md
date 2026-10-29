# ADR 0086 — A job panic is contained to one job

**Status:** Accepted · **Date:** 2026-10-28 · **Refs:**
ARCHITECTURE §8.2 (state machine), §23.6 (recovery), §28 (audit),
§31 (testing); [ADR-0060](0060-shutdown-semantics.md) (abandon-and-recover);
`docs/known-issues.md` (post-mortem discipline).

## Context

The engine runs one goroutine per job, and every phase can call third-party
parsers: tree-sitter division, readability extraction, SQLite, and the LLM
verdict parsers. A panic in any of those paths is not a normal error and does
not pass through the engine's `step` error handling. Go terminates the whole
process on an unrecovered panic, so a single malformed input or a nil map in
one job could take down the daemon, every in-flight job, the HTTP surface, the
power supervisor, and the daydream loop. Recovery from the crash exists, but a
process-wide outage is the wrong failure mode for a system whose stated posture
is continuous operation.

There was no `recover()` in production code before this decision.

## Decision

1. **`Engine.Run` recovers a panic from its own job goroutine.** The recovery
   is per job, not process-wide and not swallowed silently.
2. **The failure is committed and auditable.** The handler records a
   `job_panicked` event with the phase and a bounded stack, transitions the job
   to `failed` when it is still active, writes a runtime-error
   `CorrectionRecord`, clears the job's context-suppression scratch, and stops
   the job's pod.
3. **The handler is detached and bounded.** It uses a 10-second background
   context so a canceled phase context cannot prevent the post-mortem rows, and
   it has its own `recover` guard so a second panic cannot escape.
4. **The process stays up.** The job's goroutine returns; the daemon, its
   server, and every other job continue.
5. **The state machine is unchanged.** `failed` is a legal terminal transition
   from every active phase; the existing crash-recovery and scheduler terminal
   seams apply exactly as they do to an ordinary phase error.

## Consequences

- A parser or phase bug costs one job and is visible in the event log, not a
  daemon restart. The crash path remains the backstop for an unrecoverable
  runtime fault (for example, a CGo segmentation fault, which `recover` cannot
  catch).
- The failure is not hidden: the event carries the panic value and a bounded
  stack, and the correction feeds the ordinary feedback system.
- Tests that deliberately panic a phase now prove containment
  (`internal/engine/panic_test.go`); a future refactor that removes the
  recovery fails that test rather than silently reverting the posture.
- The handler performs database work, so it can itself fail. That failure is
  logged and the pod is still stopped; the next boot's orphan sweep is the
  backstop, as with an ordinary crash.

## Alternatives rejected

- **Let the process crash and rely on `Recover`.** Correct but loses every
  in-flight job and the HTTP surface; recovery is the last resort, not the
  first.
- **Recover in the HTTP handler only.** Engine jobs do not run on request
  goroutines; the panic would still escape.
- **Swallow the panic and leave the job running.** The job would be stuck in a
  phase with no owner and no terminal event.
- **Re-panic in tests only.** A build-tag/test-only path is easy to drift from
  production and would not prove the shipped behavior.
