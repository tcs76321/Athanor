# ADR 0061 — A harder, deterministic-first benchmark corpus

**Status:** Accepted — lands with the F4 follow-up · **Date:** 2026-10-06 · **Refs:**
ROADMAP F4/§7; [docs/f4-plan.md](../f4-plan.md);
[G-F4 closeout](../probes/gf4-closeout.md); `eval/bench/`

## Context

The 10-model G-F4 probe (2026-10-06) was the first run with real code tests and
a full size/family ladder. Its headline was a **ceiling**: on the locked
M3-T7 10-goal set, code goals scored 1.00 in both the dialectical and
single-shot arms for nearly every model, and the strongest models saturated on
every goal. Two consequences followed:

1. **The probe could not measure the loop.** With no headroom, "N-candidate
   divergence + comparison beats single-shot" is untestable — both arms pass.
2. **The probe could not measure judge discrimination.** The anchor scores
   judge *absolute* agreement; it does not order a generator's own
   near-identical candidates, which is the correlated-error property ADR-0045
   actually guards against.

The unambiguous positive result was that **deterministic verification carried
acceptance** (42/42 code accepts decided without the LLM judge), while the LLM
judge was reliable but undiscriminating and inconsistently scaled. An
evaluation corpus should therefore be scored deterministically first, and sized
so capable models do not all pass.

## Decision

Adopt `eval/bench/` as the project's task corpus:

- **Deterministic ground truth.** Every task has a real test command (code) or
  structural/schema/count checks (other archetypes); acceptance never depends
  on the LLM judge.
- **Headroom by construction.** Difficulty comes from multi-file structure,
  edge cases, exact-output requirements, multi-constraint prompts, long
  context, and adversarial specs. Tiers M and H (the M3-T7 10-goal set remains
  the E baseline), with H designed so a 9–12 B model passes well under half.
- **Anchor pairs.** Each M/H task ships a good and a near-miss artifact with
  objective labels, so judge quality is measured on *within-task ordering*, not
  cross-case scoring.
- **Archetype breadth** (code, document, text, data) plus an adversarial task
  that rewards flagging a contradiction over confident fabrication.
- **Judges advisory.** The corpus records LLM-judge scores but never lets them
  gate acceptance; the human anchor rating is the durable quality signal.

The corpus is machine-readable (`eval/bench/tasks.yaml`) so the probe can load
it rather than hardcoding goals.

## Consequences

- Future probe runs can measure the loop's value where headroom exists, and can
  calibrate judges on within-task ordering.
- The corpus is a specification first: fixtures, test suites, and anchor pairs
  must be authored before it is runnable. That is deliberate — it makes the
  benchmark reviewable before model time is spent.
- The M3-T7 10-goal set is retained as the **easy/baseline** reference; this
  corpus is the discriminating tier above it.
- No engine change is required; the probe gains a loader and the fixtures.

## Alternatives rejected

- **Keep the M3-T7 set.** It is measurable but saturated; it cannot answer the
  questions the gate asks.
- **An LLM-judged benchmark.** Circular: the judge is the thing under test.
- **Purely synthetic/adversarial tasks.** They measure robustness, not the
  loop's value on real work; this corpus keeps realistic tasks and adds one
  adversarial case.
- **A large third-party benchmark (SWE-bench-style).** Heavy, network- and
  toolchain-dependent, and not local-first; the corpus stays small, local, and
  deterministic.

## Not in scope

- Automation of the corpus into CI (a fixture-authoring follow-up).
- Media/data archetype depth beyond one data task.
- Human-label collection tooling beyond the existing `eval/anchor` protocol.
