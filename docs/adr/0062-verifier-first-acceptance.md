# ADR 0062 — Verifier-first acceptance and the judge's scope

**Status:** Accepted · **Date:** 2026-10-06 · **Refs:**
ARCHITECTURE §13.1, §19; [ADR-0045](0045-verification-first-selection.md),
[ADR-0046](0046-judge-protocol.md), [ADR-0049](0049-model-onboarding-qualification.md);
[G-F4 closeout](../probes/gf4-closeout.md)

## Context

Verification-first selection landed in F4 (ADR-0045) but was **opt-in**:
`execution.policy.judge_mode` defaulted to `llm`, so out of the box every
comparison was decided by the LLM judge. The 10-model G-F4 probe (2026-10-06)
made the cost of that default concrete and one-sided:

- **Deterministic verification carried acceptance.** 42/42 code accepts were
  decided by real tests with the LLM judge never called.
- **The LLM judge is reliable but undiscriminating.** Confidence was
  saturated (T-b: every comparison ≥0.9, observed 1.00); verdicts were unstable
  (T-c: only 40% 3-same); anchor agreement ranged 0.57–0.81 and judge scales
  ranged 0.45–0.99 across 10 models — reliability without discrimination.

The probe's unambiguous positive result and its most robust negative result
both point the same way: **the acceptance decision should be deterministic
wherever a parser can make it, and the LLM judge should be an advisory
tiebreaker, not the default gate.**

## Decision

**1. Verification-first is the shipped default.** `execution.policy.judge_mode`
defaults to **`verifier`** (was `llm`). Where a hard verifier applies, it
decides acceptance; a failed hard verifier is a reject and cannot be
overridden by the LLM (the F4-T4 reward-hacking guard). Setting
`judge_mode: llm` restores an LLM-decided comparison.

**2. The judge is advisory and scoped.** The LLM judge is consulted **only when
no hard verifier is decisive** — ties, or archetypes with no deterministic
verifier. When it is consulted, acceptance still requires the existing
safeguards: cross-family by default (`require_cross_family`), optional quorum
(`judge_count > 1`), and the §19.3 confidence guard. `min_judge_confidence`
remains but is documented as **non-discriminative** (ADR-0046 §5): it is a
floor a degenerate confidence cannot be blamed for missing, not a quality
oracle.

**3. Non-verifiable archetypes are explicitly advisory.** `data` and `media`
have no deterministic verifier today, so their acceptance is judge/LLM
advisory. Closing that — by adding verifiers or HITL-gating acceptance — is a
recorded follow-up, not a claim of safety the probe did not support.

## Consequences

- A code job whose tests pass is accepted without spending an LLM comparison
  call; a job whose tests fail is rejected regardless of the judge. The
  probe's strongest, most repeatable result becomes the default behavior.
- The F5 acceptance gates compose with this: `require_tests_for_code` fails a
  code candidate with no test run, so "no verifier applied" cannot silently
  fall through to an accepting judge for code.
- The change is a **default**, not a removal: `judge_mode: llm` and the
  comparison path remain fully supported and tested
  (`TestRun_FullDialecticalChain_ThreeCandidates_CodeArchetype` pins the LLM
  path; `TestRun_CodeArchetype_VerifierFirstByDefault` pins the new default).
- `config.example.yaml` and the ARCHITECTURE/DEVELOPMENT references are updated
  to `judge_mode: verifier`.

## Alternatives rejected

- **Keep `llm` as the default.** It ships an unvalidated judge as the gate the
  probe showed is not trustworthy.
- **Remove the LLM judge.** It is still needed for ties and for archetypes with
  no verifier; quorum and the reward-hacking guard bound the damage.
- **Raise `min_judge_confidence`.** Measured non-discriminative (confidence is
  ~constant); raising it rejects almost everything or nothing.
- **Wait for a better judge model.** The project already treats model choice as
  a river; the acceptance decision should not depend on it. Judge *onboarding*
  (ADR-0049) is the place to improve the signal.

## Not in scope

- Verifiers for `data`/`media`, and HITL-gating their acceptance (a follow-up).
- Judge-model onboarding/qualification (ADR-0049, proposed).
- Any change to the §19.3 deterministic guard's formula or the public
  `judge_mode` values.
