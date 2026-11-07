# ADR 0110 — Learned candidate-count selection (UCB)

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §13.3–§13.4, ADR-0064 (cognitive operations), ADR-0067
(technique guidance), ADR-0044 (compute policy); `internal/policy`,
`internal/strategy`, `internal/engine`

## Context

The compute policy currently decides the candidate count from the difficulty
hint, the operator ceiling, and a hard-task raise. Strategy outcomes have been
captured since M6-T10 (`strategy_profiles`/`strategy_outcomes`), and the
analysis engine mines insights, but the **selection** of how many candidates
to run is still a fixed heuristic. The system records its experience; it does
not yet use it to choose the next attempt.

The AGENTS framing is explicit: the repertoire ships whole, and what improves
is *when, where, and for whom* a technique is invoked. Candidate count is the
smallest, safest selection decision: it is bounded, visible, reversible, and
directly comparable across jobs.

## Decision

Add a **data-gated UCB1 selector** over candidate-count arms.

- **Input.** `strategy.Repo.CandidateStats(archetype, limit)` groups recent
  outcomes by the divergence phase's candidate count, read from the profile
  signature captured at job start. The signal is the accepted-new rate.
- **Rule.** `policy.ChooseCandidates` uses `acceptRate + sqrt(2 ln(total)/n)`.
  An arm is eligible only after `LearnedSelectionMinSamples` (8) observations;
  the fallback is the planner's current count. Ties keep the fallback. The
  exploration term keeps a rarely tried arm eligible, so the repertoire never
  collapses around an early winner.
- **Scope.** Applied only on a **first attempt** and only when
  `execution.policy.learned_selection` is true (the default). Retries keep the
  escalation ladder (ADR-0074). The learned choice is bounded by the operator
  ceiling and cannot raise a hard-task plan past its configured ceiling.
- **Audit.** A changed choice writes `learned_candidate_selection` with the
  from/to counts, the arm statistics, and the reason. No silent policy change.
- **Pure core.** The selector is a pure function in `internal/policy`; the
  engine only supplies the observed arms. It is unit-tested independently of
  the database.

## Consequences

- With no history the plan is byte-for-byte unchanged: the selector is a
  no-op until an arm reaches eight samples.
- Once evidence exists, a task class that reliably accepts at one candidate
  stops paying for three, and a class that needs more keeps/earns it.
- The selection is a *river*: it can be reverted by setting
  `learned_selection: false` or by the novelty reset, and it never touches
  containment, the judge, or HITL.
- This is the first selection channel. The same pattern (pure selector +
  observed arms + audit + config gate) is the template for persona, model
  family, and verification-order selection.

## Alternatives rejected

- **Change the candidate count from the LLM judge's take.** The judge is
  advisory and saturated; the outcome label is the grounded teacher.
- **Always use a bandit without a sample floor.** Early noise would reshape
  the loop; the floor and fallback keep first use stable.
- **Prune losing arms.** The repertoire is never pruned; a losing arm remains
  eligible and can recover with more evidence.
