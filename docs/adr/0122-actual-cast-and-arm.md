# ADR 0122 — Actual cast and actual arm in strategy evidence

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:** ADR-0040, ADR-0041,
ADR-0104, ADR-0110; `internal/engine`, `internal/strategy`, migration 0039

## Context

The model pool became the primary selection layer, but two pieces of evidence
still described the plan rather than the run:

- the cross-family gate read the legacy persona registry, so a pool-selected
  judge could be misreported as same-family or cross-family;
- a strategy profile recorded the planned candidate count at job start, while
  one-shot-first, the non-code cap, or a measured panel changed the number
  that actually ran. Learned candidate selection therefore learned from the
  wrong arm. `Retries` and reflection loops were also never populated (the
  latter because scratch was cleared before capture).

## Decision

Record the evidence the run actually produced:

- The acceptance gate resolves the generator and judge through the composition
  selector and compares their real families. The registry is only a fallback.
- `strategy_outcomes.candidates` stores the first divergence cycle's actual
  candidate count from the `divergence_start` audit row. `CandidateStats`
  prefers it and falls back to the profile for legacy rows.
- `retries` is derived from the job's position in its task's job set.
- `reflection_loops` is captured before per-job scratch is cleared.
- `TaskFeatures` is populated with the task type at start and the planning
  difficulty hint after planning.

## Consequences

- A learned arm now names the count that actually ran.
- The cross-family audit is trustworthy under the primary pool path.
- Legacy profiles remain readable: missing outcome arms fall back to the
  signature.
- The strategy tables remain append-only evidence; no historical row is
  rewritten.
