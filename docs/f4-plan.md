# F4 — Adaptive Judgment & Verification

**Foundations track** (like F2/F3): cross-cutting hardening of the execution
spine, not a new user-facing capability. Sits between M6 and M7; does not
renumber M7.

**Status:** planned · **Gate:** G-F4 · **Seeded by:** the M3-T7 quality probe
(`docs/probes/m3-t7-quality-probe.md`) and its findings.

## Why

The M3-T7 probe tests the project's central hypothesis: does N-candidate
divergence + deterministic judgment beat a single candidate? The Best-of-N /
test-time-scaling literature says the answer is conditional:

- gains concentrate on **hard** tasks and are marginal on easy ones;
- **verifier/judge quality** bounds the achievable gain;
- cost grows ~**linearly in N** while benefit **saturates**;
- **correlated candidates** waste N;
- **verification ≫ generation** in leverage.

A fixed N=3 on every task pays the cost everywhere and benefits nowhere in
particular. F4 makes the system's compute a **decision**, and its selection a
**verifiable** act. It is the mechanism by which the probe's result changes
future behavior rather than sitting in a findings file.

## Principle

Security constraints are mountains; **N, judging, strategy, and budgets are
rivers**. F4 changes the rivers. It must not weaken any invariant (§4),
Gate G0–G6, or the containment guarantees.

## Tasks

| ID | Task | Size | Type | Acceptance criteria | Refs |
|---|---|---|---|---|---|
| F4-T1 | **Policy/mechanism split.** Extract a pure `Policy` that maps task features + archetype + history + remaining budget + power state → a compute plan `{N, reflection loops, judge count, judge mode}`. The engine consults the policy instead of hard-coded constants; the default policy reproduces today's behavior exactly. | L | code | Same inputs → same plan; default plan is byte-identical to current behavior (regression test); the engine contains no remaining hard-coded N/reflection constants | §13 |
| F4-T2 | **Adaptive compute.** Easy/familiar tasks plan to N=1; hard/novel tasks plan to N=k. Difficulty comes from the planning phase + prior `StrategyOutcome` history for the task class. | M | code | A task the planner marks easy runs N=1; a hard one runs N=k; the choice is audited (`compute_planned`) | §13, §13.4 |
| F4-T3 | **Verification-first selection.** Per-archetype verifiers decide acceptance; the LLM judge runs only on ties or when no verifier exists. Code: tests/lint/type-check. Text/document: structural checks. Data: schema validation. | L | code | ≥ a configured fraction of code accepts are decided by deterministic verifiers, not the LLM; a judge-only path is audited | §19 |
| F4-T4 | **Judge hardening.** Calibrate judges from the T-b reliability curve; cross-family **quorum** (2-of-3) for high-stakes accepts; a **reward-hacking detector** that distrusts a judge whose score contradicts a deterministic verifier. | L | code | A miscalibrated judge's borderline verdicts are rejected; quorum only fires on high stakes; a judge/verifier contradiction is audited and resolved in the verifier's favor | §13.4, §19 |
| F4-T5 | **Heterogeneous diversity.** Generate candidates from genuinely different sources (`main` + a different-family `alternative`, distinct plans/scaffolds), and enforce a Jaccard **floor** with a bounded re-roll. | M | code | Measured per-cycle candidate diversity is above the floor on the probe's tasks; a below-floor set is re-rolled at most k times and the outcome audited | §10.1, §13.2 |
| F4-T6 | **Cost-aware acceptance.** Quality-per-token and quality-per-minute become first-class: a tie on quality at materially lower cost wins. | M | code | A lower-cost artifact wins a quality tie; the comparison audit row carries cost; the default remains backward-compatible | §28.2 |
| F4-T7 | **Reduce & complete.** Audit and simplify low-ROI complexity (the §10.5 KV tier ladder if the probe shows context is not the bottleneck; reflection gated by failure type), and finish the feedback→policy channels (persona-plan bias, template ranking, ExplorationPath proposals). | L | code | Each simplification is measured (no quality regression on the probe tasks); mined insights demonstrably bias a subsequent persona plan, audited | §10.5, §13.4 |
| F4-T8 | **Bounded inference, enforced.** Every model call carries an explicit wall-time and output-token bound; `think` is a per-phase decision (judgment phases off; generation per config). A degenerate generation can never stall a job. | M | code | A deliberately runaway call is cut and fails fast; no call path lacks a token bound; the bounds are recorded per `llm_call` | §13.1 |

Effort target ≤4h per task (S/M); `L` is the hard ceiling and may split.

## Gate G-F4

After F4, the following are enforceable and tested:

- **Compute scales with difficulty.** An easy task class converges to N=1 in an
  automated test; compute-per-accepted-artifact on the probe's easy class drops
  without an accept-rate regression.
- **Selection is verifiable.** Deterministic verifiers decide the majority of
  code accepts; the LLM judge is a tiebreaker, and a judge/verifier
  contradiction resolves to the verifier.
- **Judgment is bounded.** No model call can stall a job; every call has a
  token bound and, where relevant, `think` disabled.
- **Diversity is measured.** Per-cycle candidate diversity is above the floor;
  near-duplicate sets are re-rolled and audited.
- **The loop learns.** A mined `StrategyInsight` demonstrably affects a
  subsequent persona plan, with an audit row.

## Out of scope / deferred

- Cross-task population search (FunSearch/AlphaEvolve-style) — Athanor runs a
  bounded per-task loop, not a global evolutionary optimizer.
- Cloud escalation is already designed (§21.7) and HITL-gated; F4 makes the
  policy able to *choose* it, but the mediation path stays a separate task.
