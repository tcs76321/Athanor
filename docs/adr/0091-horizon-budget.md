# ADR 0091 — Work draws from a horizon budget with an interactive reserve

**Status:** Accepted — first slice implemented · **Date:** 2026-10-29 ·
**Refs:** ARCHITECTURE §17.2 (daydream constraints), §24 (power and idle),
§28.2 (token accounting); [ADR-0051](0051-power-supervisor.md) (power
supervisor), [ADR-0072](0072-single-slot-execution.md) (single-slot execution),
[ADR-0076](0076-continuous-evidence.md) (continuous evidence),
[ADR-0077](0077-two-day-cycle.md) (continuous operation),
[ADR-0090](0090-task-analysis-contemplation-budget.md) (contemplation budget).

## Context

Athanor's intended operating mode is continuous: it runs overnight, interleaving
operator work with memory consolidation, repository indexing, and daydreaming,
and it pauses for sleep or battery. The soak is a **measurement instrument** — a
bounded batch of independent jobs with a fixed pass count — not that mode.

The engine has runaway guards, not budgets:

- `inference.max_output_tokens` caps one call;
- `execution.phase_wall_time_budgets` caps one phase;
- `limits.max_tasks_per_hour` is parsed and **not enforced**
  (`ARCHITECTURE.md:1919`);
- daydreaming is capped per job (2 concurrent, 30 min each), not per night;
- `power.*` pauses and resumes work but does not account for what it spends.

So there is no currency in which "how much work may this night contain" can be
expressed. Every per-job allowance is therefore unbounded in aggregate: a night
of 200 jobs grants 200 allowances. That is the soak shape leaking into the
product. The same gap lets background work starve operator work, because nothing
reserves capacity for the human.

The accounting unit is a real choice:

- **tokens** are already recorded per call and per phase, and are comparable
  across model sizes better than wall time;
- **wall time** is the honest cap for latency, heat, and "the night is over";
- **energy** is the physical truth, but there is no portable joule source yet —
  the power adapter reports AC/battery, not consumption.

## Decision

1. **A horizon is a fixed accounting window, not a job.** Default 24 h, with a
   configurable boundary (`budget.window_start`, default evening) so "a night"
   and "a day" are meaningful and the reset is predictable to the operator. The
   window is fixed rather than rolling: a rolling window makes the reset time
   unpredictable and lets a job started minutes before the boundary receive a
   second full allowance.

2. **The unit is tokens; wall time is a second cap.** A window has
   `tokens_per_window` and `wall_time_per_window`; a grant must fit both. Energy
   accounting is layered on later through the power adapter, not guessed now.
   Every grant records the model and wall time so a future joule model can
   re-price history.

3. **Two buckets with a protected reserve.**
   - `interactive` may spend up to the full window ceiling.
   - `background` (indexing, consolidation, daydreaming) may spend only up to
     `ceiling − interactive_reserve`.
   - The reserve is a floor for operator work, not a quota it must use.
   Background therefore cannot consume the capacity an interactive job may need.

4. **Grants are reserved, settled, and released.** A job requests an allocation
   (tokens and wall time, plus the contemplation allowance from
   [ADR-0090](0090-task-analysis-contemplation-budget.md)). The admission path
   grants `min(request, bucket remaining, per-job cap)` and **reserves** it
   before work starts. On completion the spend is settled from `llm_call` token
   accounting and the unused remainder is released. Reserve-then-settle prevents
   overcommit without relying on single-slot execution as an implicit lock.

5. **Interactive arrival preempts background.** A queued or newly created
   interactive job closes background admission. An in-flight background job
   yields at the next phase boundary (the same edge the power gate uses), keeps
   its state, releases its unspent grant, and is requeued — it does not fail.

6. **Exhaustion defers, it does not fail.** When the bucket is spent:
   - background work is requeued with its state intact;
   - interactive work may request an extension (item 7);
   - nothing is marked failed merely because the window ended.

   `abandon` remains reserved for effort or frame exhaustion, as in ADR-0090.

7. **Ceilings rise only by an explicit, bounded, expiring grant.** The operator
   may raise the current window's ceiling. A grant is one-time, recorded with an
   actor and reason, and expires at the window boundary. A policy may *request*
   an extension; it may never grant one to itself, and no model may request or
   influence one. There is no borrowing from the next window and no automatic
   compounding — that is what "increasable but not forever" means here.

8. **Budget is a Core-owned ledger.** A `budget_ledger` table records window,
   bucket, granted, spent, released, and the actor for each adjustment; per-job
   allocations reference it. Job Pods cannot read or write it, and it is outside
   the model's tool surface. The ledger is the source of truth; events
   (`budget_grant`, `budget_exhausted`, `job_deferred`) are the audit trail.

9. **`limits.max_tasks_per_hour` is enforced in the same admission path.** It is
   a coarse count throttle, not a substitute for spend accounting, but it belongs
   at the same gate so there is one place that decides whether work may start.

10. **A benchmark must declare a budget and report whether it bound.** The soak
    harness sets an explicit window and bucket for the run, records utilization,
    and reports any job that deferred. A run whose budget binds is
    **budget-bound** and cannot support a capability claim; a run with an
    unbounded budget measures a configuration no operator has, so "unbounded"
    is not a valid benchmark setting either. The frozen corpus remains the
    compass; the budget is part of the stated conditions.

## Config shape

```yaml
budget:
  window: 24h
  window_start: "18:00"        # local; anchors the night
  tokens_per_window: 2000000
  wall_time_per_window: "10h"
  interactive_reserve_percent: 40
  background_share_percent: 60
  max_grant_tokens: 200000
  operator_grants: true        # item 7; policy may request, never grant
```

The numbers are illustrative. The invariants are: the reserve is protected, the
grant is bounded, and the ceiling is explicit.

## Implementation status

First slice (2026-10-29): migration 0031 (`budget_windows`, `budget_grants`),
`internal/budget` (window math, bucket ceilings, reserve/settle/release),
config (`budget.*`), admission before the execution slot, settlement on every
terminal path, and `limits.max_tasks_per_hour` enforced at the same gate. The
ledger is inert while both ceilings are 0.

Still open: background classification (all jobs are interactive today),
mid-job deferral (`Spend` records but does not yet pause at the boundary),
preemption of an in-flight background job, the operator grant path, and the
`/status` budget surface. A wall-time ceiling is recorded but not yet enforced
mid-job; it is reserved per grant so the aggregate is bounded.

## Consequences

- A job can now be deferred mid-flight. State preservation already exists for
  power pause/resume, so deferral reuses that path rather than adding a second
  one.
- Interactive latency is protected at the cost of background throughput. That
  trade is the purpose of the reserve, and it should be visible in the ledger.
- Tokens are an imperfect proxy for heat and energy. Recording model and wall
  time per grant keeps the history re-priceable when energy lands.
- Fixed windows create a boundary effect: work started near the end of a window
  may defer minutes later. That is preferable to a rolling reset, which hides
  the boundary and is harder to reason about.
- A window with no background spend still protects the reserve; unused capacity
  is not silently reassigned.
- The benchmark gains a condition it must state. Runs that bind are still useful
  as endurance evidence, just not as capability evidence.

## Alternatives rejected

- **Unlimited work (the soak configuration).** Measures a machine no operator
  runs, and hides the aggregate cost of per-job allowances.
- **Per-job budgets as the top scope.** Unbounded in aggregate; the exact defect
  ADR-0090 was amended to remove.
- **Wall time only.** Ignores model size: an hour of a 3B and an hour of a 30B
  are not the same spend.
- **Tokens only.** Ignores latency and heat, and lets a slow model consume a
  night in wall time without exceeding its token grant.
- **Energy first.** The honest unit, but there is no portable joule source yet;
  starting there would block the design on a platform problem.
- **Rolling window.** Smooth, but the reset is unpredictable and a job near the
  boundary can receive two allowances.
- **Model self-budgeting.** A prompt-injected model could raise its own ceiling;
  budget is a Core decision, never a model decision.
- **Fail the job on exhaustion.** Discards resumable work and makes the window
  boundary look like a reliability problem.
- **Borrow from the next window.** Compounds, and turns a bounded budget into
  debt with no natural stop.
