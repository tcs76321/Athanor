# ADR 0033 — Dependency scheduler for decomposed task graphs (M6-T2)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §7, §7.2,
§7.3, §8.2; ROADMAP M6-T2; ADR-0032 (DAG decomposition), ADR-0004 (DB
concurrency), ADR-0006 (job state enforcement).

## Context

M6-T1 ([ADR-0032](0032-dag-decomposition.md)) produces and persists a
validated task DAG but does not execute it: `goal submit` still creates a
single task and a single job, and the `tasks` table still carries the M1
status enum (`pending,ready,in_progress,blocked,done,failed,cancelled`) that
does not match §7.3 (`pending,ready,running,paused,blocked,completed,failed`).

M6-T2 has to turn a persisted graph into executed work: schedule the
dependency-free leaves, start a job per leaf, and when a job finishes either
promote its dependents to ready or mark them blocked per §7.2. It must
survive a daemon restart without losing or double-running a task, and it must
not regress the M1 walking skeleton that every existing test and the Gate G1
demo depend on.

## Decision

### 1. The scheduler is a Core component with a pure core

`internal/scheduler` owns the graph math and the orchestration:

- **Pure functions** (no I/O): `Leaves`, `Ready`, and `Blocked`, computed over
  a `[]project.Task`. `Ready` is a task that is `pending` and whose
  dependencies are all `completed`; `Blocked` is the fixpoint closure of
  tasks that depend (transitively) on a `failed` or `blocked` task.
- **`Scheduler`** holds the project repository, the job repository, and the
  engine's enqueue surface. One mutex serializes every scheduling pass, so
  concurrent terminal callbacks cannot race a read-modify-write of the task
  set.

Parent tasks (`parent_task_id`) are **grouping only**: they never receive a
job, and readiness is evaluated over leaves. This falls straight out of the
M6-T1 validation, which requires every leaf to declare acceptance criteria
while parents may omit them.

### 2. Task lifecycle: §7.3 becomes canonical

Migration 0017 rebuilds `tasks` to the §7.3 status set and adds `task_type`
(`one_time|recurring|triggered`, default `one_time`). The rebuild follows the
ADR-0005 recipe (build-new → copy → drop-old → rename-new) that migration
0004 used for `jobs`; the runner disables foreign keys for the migration and
gates the commit on `foreign_key_check`, so the inbound `jobs.task_id` /
`artifacts.task_id` references and the self `parent_task_id` reference are
preserved. M1-era values are remapped on copy (`in_progress → running`,
`done → completed`).

A task transitions `pending → running → completed|failed`, and
`pending → blocked` when a dependency fails. There are no self-loops and no
transition out of a terminal task. Since only the scheduler writes task
status, the transition table is enforced in Go (`project.CanTaskTransition`)
rather than a SQL trigger.

### 3. Completion is signalled by a nil-safe engine seam

The engine gains `SetOnJobTerminal(func(jobID string, state job.State))`. It
fires once when a job reaches `completed`/`failed`/`cancelled` — on both the
normal terminal branch and the phase-error path. A daemon that wires no
scheduler (unit tests, the M1 walking skeleton) keeps the no-op default.

Terminal handling is **idempotent**: the scheduler re-reads the task set and
only applies a status change when the task is not already in a terminal
state, and only creates a job for a `pending` leaf that has no active job.
This makes duplicate callbacks and crash-recovery replays safe.

### 4. Crash recovery is a boot reconcile pass

`Scheduler.Reconcile(ctx)` runs at daemon boot. For every goal that has
non-terminal tasks it applies any terminal job outcomes the engine may have
finished just before the crash, then schedules newly-ready leaves. The
engine's own `Recover` handles restarting the in-flight jobs; the scheduler's
pass is what advances the rest of the graph.

### 5. `goal submit` is gated, not silently changed

Wiring `goal submit` to decompose-then-schedule is behind
`execution.dag_decomposition` (default **false**). With the flag off, submit
keeps its exact M1 behavior (one task, one job) and every existing test and
demo is untouched. With it on, submit decomposes the goal and schedules the
ready leaves. The alternative — flipping submit unconditionally in M6-T2 —
would have rewritten the M1 walking-skeleton contract mid-milestone for no
capability gain; the flag lets the M6 gate demo enable the full path without
making it the default before M6-T3's failure policies land.

## Consequences

- A decomposed DAG executes: independent leaves start together (subject to
  the existing concurrency cap), dependents start only after their
  dependencies complete, and a failure blocks the affected sub-graph rather
  than the whole project.
- Task status is now observable and consistent with §7.3; the M6-T1 promise
  to reconcile the status names is discharged here.
- `goal submit` remains the M1 single-task path by default; the autonomous
  path is opt-in until M6-T3 hardens failure handling.
- The scheduler does no LLM work and executes no tools, so Gate G1 is
  unaffected; it only creates jobs and moves task rows.

## Implemented (M6-T2.2–T2.6)

Migration 0017 rebuilt `tasks` to the §7.3 lifecycle plus `task_type`;
`internal/scheduler` landed with the pure `Leaves`/`Ready`/`Blocked` core and
the mutex-serialized `Scheduler` (`Start`, `OnJobTerminal`, `Reconcile`); the
engine gained the nil-safe `SetOnJobTerminal` seam (fired on both the normal
terminal branch and the phase-error path); the daemon wires the scheduler and
reconciles non-terminal goals at boot; and `execution.dag_decomposition`
(default false) gates the decompose-then-schedule submit path. Diamond
ordering, the transitive blocked closure, idempotent callbacks, and
reconcile-after-crash are covered by tests; `goal submit` is unchanged by
default.
