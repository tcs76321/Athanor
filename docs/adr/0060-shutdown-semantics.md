# ADR 0060 — Shutdown semantics: abandon-and-recover, with prompt pod teardown

**Status:** Accepted — lands with F5 · **Date:** 2026-10-06 · **Refs:**
ARCHITECTURE §8.2, §22, §23.3, §23.6, §24; [ADR-0024](0024-engine-pod-dispatch.md),
[ADR-0051](0051-power-supervisor.md); ROADMAP F5

## Context

The daemon's job goroutines run on `context.Background()` (`Engine.Enqueue`)
and are not owned by the HTTP server. On `SIGINT`/`SIGTERM`, `serve.go`
records a shutdown event and calls `httpSrv.Shutdown(ctx)` with a 10-second
timeout, then returns. Everything else — active jobs, and their Job Pods —
was left to whatever the OS teardown did, with the M2-T5 startup sweep
reaping orphan pods on the **next** boot.

That behavior was an implicit choice, not a recorded one. The question is
whether graceful shutdown should *drain* in-flight work or deliberately
abandon it.

## Decision

**Abandon-and-recover is the shutdown semantics.** In-flight dialectical
phases are not drained, because:

- State is serialized after **every** state-machine transition and phase
  boundary (§8.2, §23.3), so the durable state is always the last committed
  phase. Crash recovery already resumes exactly there (§23.6), and the
  `interrupted` recovery flag exists for precisely this (§8.3).
- A single LLM call or a full Job Pod test suite can run for minutes on a
  cold model load. No shutdown window that a user would tolerate can bound
  it; a bounded drain would only ever *partially* finish work, at the cost of
  a second, untested code path.

**Add prompt pod teardown.** `Engine.StopPods(ctx)` stops the Job Pod of
every active job (reusing the terminal-state `stopPod` path), and `serve.go`
calls it during graceful shutdown. This frees pod resources and container
names immediately instead of waiting for the next boot's M2-T5 sweep. It
does **not** wait for, cancel, or otherwise coordinate with the running job
goroutines: a job whose pod is stopped mid-phase will fail its next pod call,
which is the abandon half of the posture. The M2-T5 startup sweep remains
the backstop for an ungraceful `kill -9`.

## Consequences

- Shutdown is fast and predictable; there is no new state for a job to be in,
  because a non-terminal job is simply a job that `Recover` will re-drive.
- A clean `SIGTERM` now leaves **zero** surviving Job Pods, not just a swept
  set on the next boot. The behavioral integration probes (Gate G2) already
  assert "no surviving pods after crash"; this extends the clean-shutdown
  case.
- The power supervisor's pause path (§24, ADR-0051) already exists for
  *planned* stops (battery, sleep) and re-drives paused jobs. Shutdown is not
  overloaded onto it: a pause is recoverable in-process, whereas shutdown
  exits the process.

## Alternatives rejected

- **Waitgroup drain bounded by `shutdownTimeout`.** Adds a wait, a cancel
  path, and partial-transition edge cases for no durable benefit: the work
  either finishes (rare within 10 s) or is discarded and redone anyway.
- **Pause every active job before exiting.** Turns shutdown into a state
  transition with its own failure modes, and a `paused_from` value that must
  round-trip through the same recovery machinery the abandon path already
  uses.
- **Do nothing (status quo).** Leaves orphan pods until the next boot, which
  is worse for long-lived hosts that restart the daemon frequently, and
  leaves the choice undocumented.

## Not in scope

- Signal handling beyond `SIGINT`/`SIGTERM` (e.g. `SIGHUP` reload) — unchanged.
- Draining network fetches or in-flight HTTP requests — `httpSrv.Shutdown`
  already handles request drain.
