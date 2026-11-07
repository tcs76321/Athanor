# ADR 0121 — One terminal finalization path

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:** ARCHITECTURE §8, §13.3,
§19, §23; ADR-0033, ADR-0035, ADR-0119; `internal/engine`, `internal/hitl`,
`internal/ui`

## Context

A terminal job must do more than change state: settle its budget, capture the
strategy outcome, write the process scorecard, clear per-job scratch, notify
the scheduler, and stop its Job Pod. Those operations lived in
`finalizeTerminal`, but three writers could commit a terminal transition
without reaching it: HITL rejection/expiry, the UI's cancel fallback for a
parked job, and a phase error that failed a job from `Run`.

The consequences were visible: a rejected HITL task could leave its DAG task
`running`, a cancelled paused job could retain a pod and scratch, and a failed
job could lack a process scorecard. The same code also cleared scratch before
capturing the outcome, which made `ReflectionLoops` zero on completed jobs.

## Decision

`Engine.FinalizeTerminal(ctx, jobID)` is the single public finalization path.
It reloads the job, refuses non-terminal states, and runs one ordered,
idempotent sequence:

1. bounded completed-job memory summary,
2. process scorecard,
3. strategy outcome (before scratch cleanup, because the reflection counter is
   per-job state),
4. scratch cleanup,
5. horizon settlement,
6. scheduler notification,
7. pod teardown,
8. generator-family cleanup.

Every terminal writer uses it: the normal loop, phase failure, cancel, panic,
HITL rejection/expiry, and the UI/API fallback. HITL gets a terminal observer
seam wired to the engine. `Engine.Cancel` also handles a parked job with no
goroutine instead of returning false.

## Consequences

- Terminal side effects are no longer an accident of which writer failed or
  cancelled the job.
- The order is part of the contract: capture before clear.
- `finalizeTerminal` remains safe to replay after a crash; each step already
  deduplicates or is idempotent.
- A future terminal writer must call `FinalizeTerminal`, not
  `jobs.Transition(..., terminal)` directly.
