# ADR 0063 — F4 closeout: an inconclusive gate, and the move to integrated validation

**Status:** Accepted · **Date:** 2026-10-07 · **Refs:**
ROADMAP F4/§3; [f4-plan](../f4-plan.md);
[G-F4 closeout](../probes/gf4-closeout.md);
[ADR-0045](0045-verification-first-selection.md),
[ADR-0046](0046-judge-protocol.md),
[ADR-0049](0049-model-onboarding-qualification.md),
[ADR-0061](0061-harder-benchmark.md),
[ADR-0062](0062-verifier-first-acceptance.md)

## Context

F4 set out to make the measurement instrument trustworthy (real code tests,
reliable and non-degenerate judges, agreement with a human-anchor set) and
judgment bounded and cost-aware. It landed every task (T0–T8), and the
10-model probe of 2026-10-06 returned an asymmetric result:

- **Met emphatically.** Deterministic verification carried acceptance:
  **42/42 code accepts** were decided without the LLM judge; all **10/10**
  judges were reliable on the anchor; diversity was real (0.45–0.74);
  judgment was bounded on every call; compute scaled with difficulty.
- **Not met — for an instrument reason.** The LLM judge is reliable but
  **undiscriminating**: comparison confidence saturated (mean 1.00), verdict
  stability was only **40% 3-same**, anchor agreement ranged **0.57–0.81**,
  and judge scales varied widely (`muse-glimmer:30b` 0.45 vs `gemma4:12b`
  0.99). The human-anchor rating (P4) is still provisional. And the
  **M3-T7 task set saturated** — code scored 1.00 in *both* the dialectical
  and single-shot arms for nearly every model — so the central hypothesis
  ("does N-candidate divergence beat single-shot?") cannot be answered on it.

The bottleneck F4 exposed is **measurement resolution, not more judge code**.

## Decision

**1. Close G-F4 as inconclusive, not passed.** The trust arm is unresolved
for an instrument reason (a saturated task set and a provisional anchor),
not a capability reason. `ROADMAP.md` records G-F4 as *closed —
inconclusive*, pointing at this ADR. The code response to the finding
already shipped: verifier-first acceptance by default (ADR-0062).

**2. Stop iterating F4 in place.** Further tuning on a saturated instrument
measures noise. The open questions — judge discrimination, human-anchor
agreement, and the loop's value — move to the successor milestone **M8
(Integrated Validation & Cognitive Regulation)**, whose harder,
deterministic-first corpus (ADR-0061) is the instrument F4 lacked.

**3. Preserve the findings and own the residuals.** The judge stays advisory
(ADR-0045/0046); model onboarding/qualification (ADR-0049, proposed) remains
the structural path to a trustworthy judge, and it is now a downstream
output of M8's trajectory data rather than an F4 task.

**4. Fix the framing of the successor.** M8 asks not "is the dialectical
engine good?" but **"which cognitive operations earn their keep, where, and
for whom"** — an ablation over the harder corpus, with selection learned
from grounded outcomes (tests, failing APIs, schema checks), human evals as
calibration, and the judge advisory.

## Consequences

- A future finding that the judge *is* discriminating is new evidence, not a
  retroactive pass: "inconclusive" is specific, dated, and has a named owner.
- The repertoire of cognitive operations is never pruned; what is learned is
  **selection** (when/where/for whom), regulated by budget. See `AGENTS.md`
  §Terminology.
- Advisory acceptance for archetypes without a deterministic verifier
  (`data`, `media`) is tracked as M8 work, not assumed safe.
- G7's remaining human arms (soak, fresh-install, §31.3 audit) are absorbed
  as evidence from M8's combined validation run, so G7 and G8 close together.

## Alternatives rejected

- **Keep F4 open indefinitely.** It blocks M7/M8 on a measurement the current
  instrument cannot make.
- **Declare G-F4 passed.** It asserts instrument trust the probe contradicted.
- **Remove the LLM judge.** It is still needed for ties and verifier-less
  archetypes; cross-family, quorum, and the reward-hacking guard bound it.
- **Raise `min_judge_confidence`.** Measured non-discriminative (ADR-0062).
