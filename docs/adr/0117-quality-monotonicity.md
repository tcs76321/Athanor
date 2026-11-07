# ADR 0117 — Quality monotonicity over compute

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §13.1, §19.3; ADR-0074 (attempt escalation), ADR-0116
(benchmark portfolio); `internal/engine`, `tools/bench`

## Context

The dialectical loop spends more compute than a one-shot call: N candidates,
evaluation, reflection, self-refine, comparison. That extra compute may buy
nothing on an easy task. That is acceptable: **equal quality at higher cost is
a tuning problem**. It is not acceptable for the full loop to deliver a
**worse** artifact than a one-shot call with the same model and prompt.

The sequence diagnostic exposed two ways this could happen:

1. When no candidate passed, `bestPassingCandidate` returned the **last**
   candidate, not the best-scoring one. Synthesis could start from an
   arbitrary, weaker candidate.
2. Synthesis could rewrite a non-passing candidate into something worse, and
   the old fallback only preserved a candidate that had already passed.

A one-shot arm is a single candidate; a full arm can pick a worse seed. That is
a design bug, not an acceptable cost/quality trade.

## Decision

Make quality **monotonic** over compute:

- **Seed from the best attempt.** When no candidate passes, synthesis starts
  from the highest-scoring candidate, not the last one. The evaluation records
  are the source of the score; a candidate with no record is skipped.
- **Never degrade the delivered artifact.** After synthesis, if the final
  artifact fails a hard verifier, fall back to the source candidate when the
  candidate passed, hard-passed, or scored higher than the rewrite. This is
  the `shouldFallbackToCandidate` rule.
- **Benchmark tripwire.** The paired report shows `single` and `full` pass
  rates, tokens, the token multiple, and a **quality regression** flag when
  full is worse than single on the same task. A regression is an investigation
  item, not a result to report as a trade.
- **One-shot first.** The intended operating policy is: try one shot; if it
  passes, stop; escalate to N/reflection only on failure. The benchmark has a
  `cascade` arm for this; the full arm remains the unconditional N-candidate
  control so the two are distinguishable.

## Consequences

- Extra compute can produce equal quality at 2×–10× the tokens; that is a cost
  finding, and the report shows it.
- Extra compute must not produce worse quality. If it does, either the seed
  selection, synthesis, or final-artifact preservation is wrong.
- The full arm is no longer allowed to justify a regression as "more
  exploration"; the benchmark flags it explicitly.
- The one-shot arm remains a gate, not just a baseline: a task where it passes
  is saturated and is not evidence for the loop.

## Alternatives rejected

- **Accept variance as an explanation.** Variance is a reason to run more
  samples, not a reason for the full loop to deliver a worse artifact than its
  own best candidate.
- **Always return the last candidate.** It is arbitrary and was the direct
  cause of a non-monotonic seed.
- **Always accept the synthesized final.** It can be worse than the candidate
  that was already evaluated; the verifier is the floor.
