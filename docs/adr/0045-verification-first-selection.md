# ADR 0045 — Verification-first selection (F4-T3)

**Status:** Accepted — lands with F4-T3 · **Date:** 2026-10-05 · **Refs:**
ARCHITECTURE §13.1, §19, §25; ROADMAP F4; [docs/f4-plan.md](../f4-plan.md);
[ADR-0044](0044-compute-policy-seam.md); [M3-T7 results](../probes/m3-t7-quality-probe-results.md)

## Context

The M3-T7 probe found the dialectical loop generates diverse candidates
(mean pairwise Jaccard 0.795) but cannot rank them: the judge was the **same
model** as the generator, errors were correlated, the rubric saturated at
1.0, and on the human-rated subset the engine's ordering inverted twice. The
code arm was unmeasurable because the test command was a no-op.

So the bottleneck is verification, not generation. F4-T3 makes selection a
deterministic act where a parser can decide, and reserves the LLM for the
cases a parser cannot.

## Decision

**A deterministic verification layer (`internal/verify`).** A pure
`Verifier` returns a `Verdict` for one candidate. The in-tree set:

- `tests` — the Job Pod's real test run for this candidate (`TestRan`
  false ⇒ not applied; a missing runner never yields a free pass).
- `lint` — the configured linter's run, when the project declares linters.
- `structure` — structural constraints parsed from the acceptance criteria
  (word limits, exact paragraph counts, required section names, placeholder
  absence, a minimum list count in a named section). Criteria that resist
  parsing yield an **unapplied** verdict.

An unapplied verdict is silence, not a pass. The registry aggregates only
applied verdicts.

**Selection is verification-first.** In `phaseCompare`, when the compute
plan's `JudgeMode` is `verifier`:

1. Run the verifiers on the final artifact.
2. A failed verifier is decisive — reject, no LLM.
3. A clean pass with no previous accepted artifact is decisive — accept, no
   LLM.
4. Otherwise the LLM judge is the tiebreaker, and only when its persona's
   model **family** differs from the generator's.
5. A same-family judge (and `require_cross_family`) is not consulted; without
   decisive deterministic evidence the new artifact is declined and the
   mismatch is audited (`judge_family_mismatch`).

The §19.3 deterministic guard is unchanged and still runs on the LLM verdict.

**Family is declared, not guessed.** `personas.<role>.family` names the
model lineage ("qwen", "gemma4", "mistral-nemo"). The default is the model
name with its tag stripped. A smaller model from the same family is a worse
version of the same mind, not an independent judge, so the comparison is an
exact lineage match, not a size or tag comparison. The check is consulted
only on the verifier path, so a single-model `llm` deployment is unaffected.

**Config.** `execution.policy.judge_mode` (`llm` | `verifier`, default
`llm`) selects the path; `judge_count`, `verifier_min_fraction`,
`min_anchor_agreement`, and `require_cross_family` parameterize it. Every
audit decision is recorded as a `verification_decision` row
(`judge_mode`, `judge_called`, verifier applied/passed, family comparison).

**Per-candidate tests.** `runCodeInPod` now takes the candidate's bytes and
is called per candidate (previously it always executed the latest proposal,
so every `EvaluationRecord` shared one candidate's test result). This is what
makes "the verifiers decide" mean anything for N>1.

## Consequences

- Code acceptance on a fresh project is decided by the real test run, with
  no LLM comparison call — the F4-T3 acceptance metric.
- Text/document gain deterministic checks where the criteria are
  parseable; the LLM still handles tone and anything unparsed.
- F4-T4 (judge quorum/calibration) plugs into `JudgeMode`/`JudgeCount`
  without another selection rewrite.
- The `lint` verifier is applied when a project declares linters and the
  job envelope allows `lint`; otherwise it is silent. `type-check` is a
  future verifier behind the same interface.
- A bug found in passing: the final synthesized artifact is tested in
  `phaseCompare` (via the verifier) rather than only the proposals.

## Alternatives rejected

- **Trust the LLM judge harder (better prompts only).** The probe showed
  saturation and inversion; prompt tuning is a T4 calibration concern, not
  a selection architecture.
- **Use a smaller model from the same family as the judge.** No
  decorrelation — the whole point of the change.
- **Infer family from the model tag.** Tags churn and do not express
  lineage (`qwen2.5` vs `qwen2.5-coder`); an explicit declaration is
  auditable.
- **Make `verifier` the default now.** Behavior-preserving default (`llm`)
  plus an opt-in keeps every existing deployment and the probe baseline
  reproducible until T4 calibrates the judge.

## Not in scope

- Judge calibration against the human anchor and quorum (F4-T4).
- A learned/distilled verifier (future; the interface is the seam).
- Wiring `documentation_required`/`run_command` (F3 deferred flags).
