# ADR 0047 — Heterogeneous diversity and cost-aware acceptance (F4-T5/T6)

**Status:** Accepted — lands with F4-T5/T6 · **Date:** 2026-10-05 · **Refs:**
ARCHITECTURE §10.1, §13, §28.2; ROADMAP F4; [docs/f4-plan.md](../f4-plan.md);
[ADR-0044](0044-compute-policy-seam.md), [ADR-0045](0045-verification-first-selection.md)

## Context

The M3-T7 probe measured real divergence (mean pairwise Jaccard 0.795) but the
candidates were all samples of the *same* model, so their errors were
correlated and best-of-N collapsed toward best-of-1. Separately, the loop
spent ~1.8× tokens for no measurable quality gain and had no way to prefer a
cheaper artifact that was just as good.

## Decision

**Heterogeneous diversity (F4-T5).** `Plan.DivergenceRoles` is an ordered
persona list the divergence phase cycles through. When
`execution.policy.heterogeneous_diversity` (default **true**) is on and a job
has ≥2 candidates, the default policy expands the generator role to
`{generator, alternative}` so candidates come from more than one source. A
custom policy may set the list explicitly. The `divergence_candidate` audit
row now records the persona.

**Jaccard floor with a bounded re-roll (F4-T5).** Every batch's mean pairwise
Jaccard distance is audited (`divergence_jaccard`, with `floor` and
`attempt`). If it is below `execution.policy.jaccard_floor` (default 0.30) and
rerolls remain (`max_diversity_rerolls`, default **0** — opt-in), the batch is
dismissed (`draft → candidate → rejected`, so it is never evaluated; `draft`
cannot transition directly to `rejected`) and a fresh batch is generated. A
`divergence_reroll` row records each dismissal. Diversity is undefined for a
single candidate, so `n < 2` never re-rolls. The re-roll is opt-in so the
default loop pays nothing for the guard; the probe enables it.

**Cost-aware acceptance (F4-T6).** The `comparison` audit row always carries
`token_cost` and `wall_time_ms` for the job. When
`execution.policy.cost_aware` (default **true**) is on, a declined new
artifact (`winner: previous`) whose best evaluation score is within
`quality_tie_margin` (default 0.02) of the previous's average score, and which
spent strictly fewer tokens, is accepted instead; a `cost_aware_accept` row
records the comparison. Unknown (zero) costs never win. This only changes
near-ties, so a real quality gap is never overridden by cost.

## Consequences

- Non-trivial jobs now draw candidates from two personas; on a single-model
  config the two roles resolve to the same model and the behavior is inert.
- The re-roll guard is measurable and reversible; the default is off.
- Cost is a first-class tie-breaker, so equal-quality work converges on the
  cheaper route, which is exactly what the §13.3 strategy dataset needs.
- A future learned router can populate `DivergenceRoles` and the cost margin
  without another rewrite.

## Alternatives rejected

- **More samples of one model.** The probe's negative result is precisely
  that correlated samples do not diversify error.
- **Always re-roll below the floor.** Doubles cost on every low-diversity
  model; opt-in keeps the default budget bounded.
- **Accept on cost alone.** Cost without a quality tie would trade quality
  for spend; the tie margin keeps quality primary.

## Not in scope

- Learned routing/bandit selection (future milestone).
- Cross-family enforcement for *generation* (only judgment is gated).
