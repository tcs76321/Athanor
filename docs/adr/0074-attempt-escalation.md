# ADR 0074 — Attempt-level escalation and search

**Status:** Accepted — the ladder (§3) and the engine knobs are implemented; the
learned selector (§4) and the soak per-attempt curves are follow-ups ·
**Date:** 2026-10-16 · **Refs:**
ARCHITECTURE §7 (DAG), §13 (engine), §13.4 / [ADR-0067](0067-technique-guidance-loop.md)
(technique guidance), §24; [ADR-0044](0044-compute-policy-seam.md) (compute &
model policy); [ADR-0072](0072-single-slot-execution.md) (single-slot);
[ADR-0073](0073-soak-validation-harness.md) (soak).

## Context

When a job fails, §7.2 retries the **same** configuration, then re-decomposes
with the `alternative` persona, then escalates to HITL. The compute/model policy
([ADR-0044](0044-compute-policy-seam.md)) is a pure function of the *task
class*: it decides candidates, reflection budget, judge mode, and phase→persona
routing, but it cannot condition on **how many times this task has already
failed**, and by rule 4 it may *lower* compute but never raise it past a fixed
ceiling. The knobs a human would actually vary on a retry — the time budget, the
model, the temperature, the prompt — are not in the plan at all.

Meanwhile the engine already records, per job, the executed phase/persona/
temperature/candidate **signature** and the grounded **outcome**
(`strategy_profiles`, `strategy_outcomes`) and mines winning cohorts into
technique insights ([ADR-0067](0067-technique-guidance-loop.md)). What is missing
is the loop that *uses* that corpus to choose the next configuration of the task
at hand.

## Decision

1. **Attempt is first-class.** A task's execution is a sequence of **attempts**;
   each attempt is one job with its own plan. The task's §7.3 `dag.Budget`
   (`max_jobs`, `max_llm_calls`, `max_tokens`, `max_wall_time`) is the true
   ceiling; attempts allocate within it.
2. **The plan carries the escalation vocabulary.** `policy.Features` gains the
   attempt history for the task and its class (attempt index, prior
   signatures + results). The plan gains the knobs a retry varies: phase
   wall-time **budget scale**, **temperature** delta, **model/persona** override,
   a **prompt directive**, and candidate count.
3. **A fixed ladder ships as the default.** On a failed attempt, the scheduler
   asks an **EscalationPolicy** for the next plan. The default is deterministic:
   raise the time budget → raise candidates → reflection/repair budget → swap
   model/persona → adjust temperature → change the prompt directive → then
   combinations — until the task budget is exhausted. This ladder is the
   **repertoire**, always present, and the baseline the selector is measured
   against.
4. **A selector learns over the ladder.** Where grounded history exists, a
   selection layer chooses the next move (or combination) from signatures that
   won for the class, blended with `policy.Explore` randomness
   ([ADR-0067](0067-technique-guidance-loop.md) §5); the exploration floor keeps
   an unproven move eligible. This is the **selection** layer (AGENTS
   "repertoire vs. selection").
5. **Everything is tracked.** The attempt index joins
   `strategy_profiles.signature_json`, so cohort analysis sees "attempt 2 + a
   bigger budget" as a strategy and can validate the selector against the
   ladder.
6. **Sequential by construction.** Single-slot execution
   ([ADR-0072](0072-single-slot-execution.md)) makes the search a sequential walk
   over candidates, not a parallel beam.
7. **Bounded and safe.** Escalation is internal, contained, and reversible, so it
   runs autonomously within the task budget — no HITL gate (the AGENTS HITL
   boundary). Deterministic verifiers stay the accept edge: escalation may
   propose, but it cannot accept an artifact the verifier rejects.

## Consequences

- §7.2's "retry identical → re-decompose → HITL" becomes the **final** fallback,
  reached only after the ladder and the selector are exhausted within budget.
- [ADR-0044](0044-compute-policy-seam.md)'s ceiling is amended: it is a
  **per-attempt** ceiling; the **task budget** is the real bound. A policy still
  cannot exceed the task budget.
- The soak ([ADR-0073](0073-soak-validation-harness.md)) becomes the instrument:
  per (model, task) accept-vs-attempt curves and cost-to-accept show which move
  moved the needle, and tune the ladder.
- The engine's `strategy` corpus grows a new axis (attempt index and the varied
  knobs), so the existing cohort analysis and the `measure` → routing proposal
  gain attempt-aware evidence.

## Alternatives rejected

- **Keep identical retries.** Wastes the budget re-rolling a configuration the
  evidence already calls losing.
- **Jump straight to a model swap.** Ignores the cheaper levers (budget,
  candidates, temperature, prompt) that often suffice, and thrashes a
  single-resident model ([ADR-0072](0072-single-slot-execution.md)).
- **A pure learned scheduler with no fixed ladder.** No safe default to fall back
  to when history is thin, and nothing to validate the learner against.
