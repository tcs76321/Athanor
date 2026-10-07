# ADR 0064 — Cognitive operations: a shipped repertoire with learned selection

**Status:** Accepted — framework; the catalog and its substrate land across M8 ·
**Date:** 2026-10-07 · **Refs:** [ADR-0063](0063-f4-closeout.md) (F4 closeout),
[ADR-0044](0044-compute-policy-seam.md) (`policy` seam),
[ADR-0040](0040-strategy-capture.md) / [ADR-0041](0041-strategy-analysis.md)
(strategy records), [ADR-0045](0045-verification-first-selection.md)
(verification-first), [ADR-0061](0061-harder-benchmark.md) (harder corpus);
ARCHITECTURE §13, §17, §18; `docs/cognitive-operations.md`.

## Context

F4 closed inconclusive ([ADR-0063](0063-f4-closeout.md)): the probe measured
one mechanism — *best-of-N plus comparison* — on a task set that saturated, so
it could not answer whether "the dialectical engine" is good. The successor
question is narrower and answerable:

> Which cognitive operations actually earn their keep, on which tasks, for
> which user — and how does the agent learn that?

Human problem-solving is a repertoire (draft, review, rewrite, try several,
ruminate, daydream, rehearse failure, sleep on it, form habits), not one
technique. Athanor already implements pieces of this repertoire
(divergence, reflection, daydreaming, compaction, strategy mining) without a
shared model of what each operation *is*, when it applies, or how its result
is verified. Without that model, "is the loop good?" stays unanswerable and
selection stays hard-coded.

## Decision

**1. A Cognitive Operations registry is the shared model.** Every technique is
an operation that declares: inputs, cost class, **verification edge**
(how its output is judged), **termination condition**, and **containment
class** (internal-reversible vs external/irreversible). The catalog lives in
`docs/cognitive-operations.md`; the registry is the spine M8's ablation and
selection build on.

**2. The repertoire is never pruned; selection is learned.** Every operation
ships. What is learned is *when, where, and for whom* to invoke it. An
operation that underperforms for one task class is de-prioritised for that
class, not deleted — a different user's task distribution may need it.

**3. Grounded outcomes teach; human evals calibrate; the judge advises.**
Each job records a **trajectory** of which operations ran, their cost, and the
grounded outcome (tests, failing APIs, schema checks). Selection learns from
these, keyed by task class and scope (project/global). Human evaluations
calibrate; the LLM judge never gates acceptance.

**4. Selection extends the existing policy seam.** `internal/policy` already
chooses compute and routing purely and deterministically. Its `Plan` gains an
**eligibility/selection** surface over operations, biased by learned insights
and bounded by budget. The operation vocabulary is a leaf package
(`internal/cognitive`) shared by `policy` (selection) and `strategy`
(capture), so the two cannot drift and the policy seam carries no storage
dependency.

**5. Novelty resets toward the default, not toward noise.** When a project or
task class is new, selection resets prior specialisation toward the **shipped
default heuristics blended with exploration randomness** — exploit the
built-in default path, inject random exploration. If the default path
underperforms, the agent moves *away* from it along the learned gradient. This
is a per-project option (`novelty_reset: auto | reset | keep`), defaulting to
reset-and-anchor-on-default once the agent has specialised. It prevents the
"deep rut" of over-specialisation on a novel problem while never starting from
pure randomness.

**6. HITL gates only the machine boundary.** Internal, contained, reversible
work — tangents, wasted candidates, rewrites — proceeds autonomously under
budget and success tracking. Human approval is reserved for external or
irreversible actions.

**7. Cognitive regulation is mandatory.** Every operation carries a
verification edge and a termination condition. Budgets are the throttle, not a
ceiling on ambition: the agent may spend more where it helps, but no operation
may loop unbounded, and no rewrite is accepted without a verification edge.
This is the "rung on the ladder" that keeps structured iteration from becoming
a spiral.

## Consequences

- M8's probe runs an **ablation** — arms like single-shot, best-of-N,
  plan+reflect, full — over task strata (easy/medium/hard/multi-step), so the
  run reads out *per operation* rather than one verdict. "Phase it out" and
  "make it bigger" become data, not guesses.
- The learning substrate is an extension of `StrategyProfile` /
  `StrategyOutcome` / `StrategyInsight`, not a new subsystem: the signature
  gains the operations invoked, and aggregation learns per operation × task
  class × scope.
- Novelty handling becomes a first-class, user-visible option rather than an
  implicit consequence of history.
- The catalog is documentation now and a registry later; the ADR fixes the
  contract so the two cannot drift.

## Alternatives rejected

- **Prune operations that underperform.** Throws away the repertoire a
  different user may need; selection, not existence, is what should change.
- **Reset novelty to uniform randomness.** Discards the shipped default
  heuristics, which are a good prior; the reset is *default ⊕ randomness*.
- **HITL-gate internal iteration.** Internal work is contained and reversible;
  gating it would be the wrong use of the boundary and would stall learning.
- **A fixed whitelist of techniques.** Same failure as pruning, plus it cannot
  adapt to a user's task distribution.
