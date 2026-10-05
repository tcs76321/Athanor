# ADR 0034 — Task failure policy (M6-T3)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §7.2,
§7.3, §18.1, §20; ROADMAP M6-T3; ADR-0032 (DAG decomposition), ADR-0033
(dependency scheduler).

## Context

M6-T2's scheduler ([ADR-0033](0033-dependency-scheduler.md)) executes a
decomposed DAG and blocks a failed task's descendants, but it has no answer
for the rest of the §7.2 failure table:

| §7.2 failure | M6-T2 behavior | needed |
|---|---|---|
| Leaf fails after max retries | immediately blocked | retry, then re-decomposition, then HITL |
| Dependency task fails | descendants blocked | done (`Blocked`) |
| Budget exhausted for a sub-DAG | none | pause/block remaining, escalate |
| DAG decomposition fails | `tall`, then `main`, then typed rejection | done (M6-T1) |

Two of the four rows need capabilities that do not exist yet: alternative
sub-decomposition and the HITL queue (M6-T4). Building either inside M6-T3
would push an `M` task past its size ceiling and invent the M6-T4 schema
early.

## Decision

### 1. Bounded retries precede any escalation

`execution.max_task_retries` (default 2) bounds attempts per task; a task's
own `budget.max_jobs` (`§7.3`), when set, is a second, tighter bound. The
attempt count is the number of jobs created for the task — no new column,
since the job rows are the durable record. On a failed job with attempts
remaining, the task returns to `pending` and the scheduler reschedules it on
the same pass; on exhaustion it moves to `blocked`.

The scheduler distinguishes the cause (`retries_exhausted` vs
`budget_exhausted`) in the audit row so a post-mortem can tell why the task
stopped.

### 2. A cancelled job is terminal, not retryable

A user or operator cancellation is a decision, not a failure: a `cancelled`
job marks its task `failed` and starts no retry. Only a `failed` job enters
the retry policy.

### 3. Re-decomposition and HITL are seams, not T3 deliverables

The scheduler gains two nil-safe seams:

- `ReDecomposer.ReDecompose(ctx, task) (int, error)` — attempts to replace a
  blocked leaf with subtasks and returns how many it created.
- `Escalator.Escalate(ctx, task, reason) error` — surfaces the blocked task
  for a human decision.

On exhaustion the scheduler blocks the task (which the T2 closure propagates
to descendants), attempts re-decomposition when a seam is wired, and
escalates when re-decomposition is absent, fails, or creates nothing. With
no seams wired — the shipped default — it blocks and audits, which is the
honest "no policy configured" outcome and cannot silently lose work.

The **concrete** alternative-persona sub-decomposition adapter lands as
**T3b** (its own ADR note/commit), and the **real** `hitl_requests` writer is
**M6-T4**'s queue. M6-T3 tests exercise both seams with fakes, so each §7.2
row has an automated scenario now without pre-empting those designs.

### 4. Decomposition failure (row 4) is already covered

M6-T1's `tall → main` retry and typed `ErrRejected` are the §7.2 row-4
behavior; M6-T3 adds no code there and cites the M6-T1 tests.

## Consequences

- A leaf gets `max_task_retries + 1` attempts (or `budget.max_jobs` when
  smaller) before it blocks; a transient model/pod failure no longer strands
  a whole DAG.
- Exhaustion is observable: a `task_retry`, `task_redecomposed`, or
  `task_escalated` audit row records what the policy did.
- Until T3b/T4 wire the seams, exhaustion ends in `blocked` plus an audit
  row; no work is lost and no HITL row is fabricated with a schema T4 owns.
- The task lifecycle gains `running → pending` (retry) and
  `running → blocked` (exhaustion) edges; `failed` is reserved for
  cancellation.

## Implemented (M6-T3.2–T3.3)

The task lifecycle gained `running → pending` (retry) and `running → blocked`
(exhaustion) edges; `execution.max_task_retries` (pointer field, default 2,
explicit 0 disables) landed with defaults, validation, and example sync; and
the scheduler now retries a failed task while attempts remain, then blocks
and attempts re-decomposition, then escalates, reporting the cause
(`retries_exhausted` vs `budget_exhausted`) in the audit row. The
`ReDecomposer`/`Escalator` seams are nil-safe: the shipped default blocks and
audits. §7.2 rows 1–3 each have an automated scenario; row 4 is M6-T1's
`tall → main` retry. The concrete alternative-persona re-decomposition
adapter remains **T3b**, and the real `hitl_requests` writer is **M6-T4**.
