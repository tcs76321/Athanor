# ADR 0046 — Judge protocol: quorum, reward-hacking guard, anchor calibration (F4-T4)

**Status:** Accepted — lands with F4-T4 · **Date:** 2026-10-05 · **Refs:**
ARCHITECTURE §13.4, §19; ROADMAP F4; [docs/f4-plan.md](../f4-plan.md);
[ADR-0045](0045-verification-first-selection.md); [M3-T7 results](../probes/m3-t7-quality-probe-results.md)

## Context

The M3-T7 probe produced the project's hardest negative result: the judge
layer could not be trusted. The third-party judges errored on 22%/51% of
calls, saturated at 1.0 when they answered, and agreed vacuously. The
engine's own `confidence` was a near-constant ≈0.99, so the §19.3
`min_judge_confidence: 0.7` gate accepted everything. The F4-T0b protocol fix
raised reliability to 86%/89% but **saturation persisted** — a reliable
judge is not yet a discriminating one.

F4-T4 therefore hardens the judge without pretending the underlying signal is
stronger than it is. Deterministic verification (ADR-0045) is the primary
safety; the judge protocol below bounds the damage the LLM can still do.

## Decision

**1. Quorum (2-of-3) for high-stakes accepts.** When
`execution.policy.judge_count > 1`, the comparison judge runs N times and the
winner must hold a strict majority; the vote is audited as `judge_quorum`
(`votes`, `majority`). A call that cannot parse abstains. When no majority
exists, the verdict falls through to the §19.3 guard, which will not accept
an unsupported `new`. The default remains `judge_count: 1`.

**2. Reward-hacking guard.** In every judge mode the deterministic verifiers
run on the final artifact. If a verifier is decisive and **fails** while the
verdict would accept the new artifact (`winner: new`), the verifier wins: the
verdict is rewritten to `previous`/`none` and a `judge_verifier_contradiction`
row records the override. An LLM that accepts what the parser rejected is
either mistaken or gaming the rubric; the deterministic evidence is
authoritative.

**3. Anchor calibration (offline).** `internal/judge` computes Spearman's
rank correlation between a judge's scores and the human-anchor ratings in
`eval/anchor`. `execution.policy.min_anchor_agreement` (default 0.6) is the
floor a judge must clear to be considered calibrated. The anchor set is the
durable teacher: base models churn, the ratings do not.

**4. Reliability floor.** A judge whose calls fail (empty response, no
`score`, unparseable) is not a measurement. The probe's judge pass enforces a
per-judge success-rate floor (`-min-success`, F4-T0b). In the engine, a
comparison parse failure hard-fails the job rather than fabricating a verdict
(the existing M3 contract).

**5. `min_judge_confidence` correction.** The default stays `0.7` for
backward compatibility, but this ADR records that it is **not
discriminative**: the probe measured confidence clustered at 0.99, so the
threshold accepts nearly everything. The load-bearing acceptance safety is
now deterministic verification (ADR-0045) plus the reward-hacking guard;
`min_judge_confidence` is demoted from "the gate" to "a floor that a
degenerate confidence cannot be blamed for missing." Raising or replacing it
waits for the anchor-calibrated run (the probe step), so the change is made
against data rather than a guess.

## Consequences

- A miscalibrated judge's borderline verdicts cannot override a deterministic
  failure; quorum makes a single errant call non-decisive.
- `judge_count`, `verifier_min_fraction`, `min_anchor_agreement`, and
  `require_cross_family` are operator-tunable and audited.
- The probe gains an anchor-calibration pass that reports reliability and
  rank agreement per judge; a judge below the floor is reported as not
  trustworthy rather than silently averaged in.
- A future distilled/learned verifier (model-tiers note) plugs in behind the
  `internal/verify` interface and is calibrated by the same anchor math.

## Alternatives rejected

- **Raise `min_judge_confidence` now.** The probe shows confidence is
  ~constant, so a higher threshold rejects almost everything or nothing; it
  is not a discriminating knob. Fix the signal first.
- **Averaging multiple judge scores.** Averaging saturated scores stays
  saturated; quorum on the *decision* is the meaningful aggregation.
- **Trust the LLM unless it errors.** Reliability without discrimination is
  the failure the probe found. The guard makes deterministic evidence
  primary.
- **Skip the anchor and self-calibrate.** Circular: the judge would define
  its own ground truth. The human ratings are the point.

## Not in scope

- Live per-job calibration against the anchor (the anchor is offline; a
  runtime judge cannot call it per job).
- Cloud judges and cascade escalation (§21.7) beyond the `Policy` seam.
- A learned reward model (future milestone; the interface is the seam).
