# ADR 0044 — Compute policy as an explicit seam (F4-T1)

**Status:** Proposed — lands with F4-T1 · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §13, §13.4, §29; ROADMAP F4; [docs/f4-plan.md](../f4-plan.md)

## Context

The M3-T7 probe measures whether N-candidate divergence + deterministic
judgment beats a single candidate. The Best-of-N / test-time-scaling
literature says the benefit is conditional: gains concentrate on hard tasks,
are bounded by judge quality, and cost grows ~linearly in N while benefit
saturates. A fixed N=3 on every task therefore overpays on easy work and is
unable to spend more where it would help.

Today the engine's compute is fixed constants: `divergence_candidates` (default
3), `max_reflection_loops` (default 2), the `security` judge persona, and a
single judge call. These are read from config but there is no single place
that decides *how much* compute a given task should get, and no seam to change
that decision per task or from history.

F4 needs that seam. This ADR records its shape so F4-T1 can implement it and
F4-T2..T5 can build on it without re-litigating the interface.

## Decision

Introduce a pure `Policy` that answers one question: **given this task and
this context, how much and what kind of compute should the job use?**

```
Policy.Decide(TaskFeatures, Context) -> Plan
```

- **TaskFeatures** (in): archetype, criteria count, a planning difficulty hint,
  and the task's recent `StrategyOutcome` history for its class (passed in, not
  queried — the policy is pure).
- **Context** (in): remaining budget, wall time, power/profile state, whether a
  prior accepted artifact exists.
- **Plan** (out): `{Candidates, MaxReflectionLoops, JudgeMode, JudgeCount}` —
  where `JudgeMode` distinguishes "deterministic verifier decides" from "LLM
  judge decides" (F4-T3/T4).

Rules:

1. **Pure and total.** Same inputs → same plan; no I/O, no clock, no random.
   Unit-tested like `DecideWinner` and `dag.Validate`.
2. **Default preserves behavior.** The default policy returns today's values
   (N=3, reflection 2, single `security` judge). A regression test asserts the
   default plan is byte-identical to the pre-F4 engine behavior, so F4-T1 is a
   refactor, not a behavior change.
3. **Consulted once per job, at planning.** The resolved plan is recorded on
   the `StrategyProfile` (so outcomes can be attributed to it) and audited
   (`compute_planned`). The engine reads the plan thereafter; it holds no
   remaining hard-coded compute constants.
4. **Bounded by config.** The policy may choose only within operator-configured
   bounds (`execution.divergence_candidates`, `max_reflection_loops`); it can
   lower compute, never raise it past the ceiling.
5. **Rivers, not mountains.** The policy never touches security constraints,
   the judge persona's temperature (still pinned 0.0), containment, or the
   HITL rules. It allocates compute only.

## Consequences

- F4-T2 (adaptive compute) becomes "implement a policy that returns N=1 on
  tasks the planner marks easy," not an engine rewrite.
- F4-T3/T4 (verification-first, judge quorum) express their decisions through
  `JudgeMode`/`JudgeCount` rather than editing phase logic.
- Every job's compute plan is already part of the strategy dataset, so the
  probe's result directly informs the default policy.
- A `nil` policy (tests, the M1 skeleton) falls back to the default plan, so
  the seam stays nil-safe like the engine's other seams.

## Alternatives rejected

- **More config fields only.** A per-field knob (`divergence_candidates: 1`)
  cannot express "N=1 for easy, N=5 for hard, verifier-first when tests exist"
  as one coherent decision, and cannot consume outcome history.
- **Inline heuristics in the phase code.** Untestable in isolation, and it
  would spread the compute decision across six phases.
- **A learned/bandit policy now.** Premature; F4-T2 starts with a
  deterministic difficulty rule and the strategy-mining channel (F4-T7)
  supplies the evidence to evolve it later.

## Not in scope

- The specific heuristic in the default adaptive policy (F4-T2).
- Verifier implementations (F4-T3) and judge quorum/calibration (F4-T4).
- Any cross-task search; the policy still scopes one job.
