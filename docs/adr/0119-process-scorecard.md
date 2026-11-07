# ADR 0119 — Process scorecard at job termination

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §13, §19, §28; ADR-0109 (notebook), ADR-0117 (quality
monotonicity), ADR-0118 (one-shot first); `internal/process`, `internal/engine`

## Context

Athanor evaluates **artifacts** and records **outcomes**, but it does not
evaluate the **process** that produced them. The strategy outcome says whether
the artifact was accepted and what it scored; the event log says what happened;
the notebook says what the model thought. None of them answers the question
that matters for real-world adaptability:

> Did the extra process buy anything?

Without that, the system cannot learn when to try one shot, when to escalate,
when a replan helped, or when it paid for a panel that was never needed. A
quality regression can hide behind a "completed" job. A rescue can look
identical to a one-shot success.

## Decision

Add a deterministic **process scorecard** at terminal state.

- `internal/process.Evaluate` is pure and total. It takes the trajectory
  (cycles, candidates, repairs, reflections, replans, observations, memory
  hits, tokens, wall time, final/best scores) and returns:
  - `OneShotSufficient` — accepted, one candidate, no escalation;
  - `Escalated` — more than one cycle or any repair/reflection/replan/
    observation;
  - `Rescued` — escalation ended in an accepted artifact;
  - `WastedCompute` — accepted, no escalation, but more than one candidate was
    generated;
  - `Regression` — accepted final scored below the best candidate.
- The engine fills the input from the event log, proposal artifacts, and
  evaluation records. It writes one `process_eval` audit row per job,
  idempotently (an existing row makes the call a no-op).
- The scorecard is **observational**. It does not change acceptance, budgets,
  or prompts. It is the evidence the selection layer needs.
- This is the runtime counterpart to the benchmark's paired report: the
  benchmark asks whether full beats single across a corpus; the scorecard asks
  whether this job's own process was worth its cost.

## Consequences

- Every job now carries a process verdict: one-shot sufficient, escalation
  rescued, panel sufficient with no escalation, failed after escalation, or
  quality regression.
- `WastedCompute` and `Regression` become visible signals for tuning; they are
  not hidden behind a completed job.
- The learned-selection work can consume these rows as grounded outcomes for
  "when to escalate" rather than only "what candidate count won."
- A failed job still gets a scorecard; failure without escalation and failure
  after escalation are different signals.

## Alternatives rejected

- **Infer the process from the final artifact only.** It cannot distinguish a
  rescue from a one-shot success, or a wasted panel from a necessary one.
- **Use the LLM judge to grade the process.** It is advisory and expensive;
  the counts and scores are deterministic and already recorded.
- **Add another table.** The event log is append-only and already the audit
  trail; a `process_eval` row is sufficient and requires no migration.
