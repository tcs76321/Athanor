# ADR 0100 — Acceptance and rejection are evidence-backed end to end

**Status:** Accepted · **Date:** 2026-11-05 · **Refs:** ARCHITECTURE §13,
§18, §19, §25; [ADR-0045](0045-verification-first-selection.md),
[ADR-0062](0062-verifier-first-acceptance.md),
[ADR-0065](0065-multi-file-code-artifacts.md),
[ADR-0092](0092-benchmark-label-integrity.md).

## Context

Three gaps undermined the "deterministic verification decides acceptance"
claim:

1. **Cost-aware acceptance could undo a verifier failure.** It ran after the
   §19.3 guard and the reward-hacking guard, and used the highest proposal
   score. A failed real test could therefore be overturned by a lower token
   cost.
2. **A single-family install could not accept a follow-up code artifact.** A
   hard verifier pass was decisive only with no previous accepted artifact. With
   a previous artifact and no cross-family judge, every later artifact was
   declined under a "completed" job.
3. **Per-candidate tests could see another candidate's work.** `/tmp/work` was
   copied once per pod and never reset, so candidate N+1 inherited candidate N's
   files and test output.

## Decision

1. **A decisive hard-verifier failure is final.** The cost-aware tie-break runs
   only when the final artifact has no hard-verifier failure, both sides have a
   known score, and the final artifact's own record (not a proposal's) is the
   new-side score.
2. **A hard-verifier pass is decisive for a follow-up when it meets or beats
   the previous accepted score.** If no cross-family judge is available, the
   deterministic pass is accepted with an explicit
   `judge_family_mismatch` audit row; a single-model-family install can keep
   doing verified work instead of silently rejecting every later task.
3. **The Job Pod work tree is reset per candidate.** `RunCode` discards
   `/tmp/work`, re-materializes the repository snapshot, and stages this
   candidate. `RunTests`/`Lint` never reset. If `execute_code` is refused,
   `RunTests` does not run against a pristine tree; the `require_tests_for_code`
   gate rejects the candidate instead.
4. **Rejections produce feedback.** Evaluation rejections capture
   `test_failure`, `security_scan`, or `evaluator_failure`; comparison
   rejections capture `evaluator_failure`; the regulator captures
   `loop_detection` and `budget_exhaustion`. User-driven sources stay on the
   UI/CLI rejection form. `hallucinated_path` still needs a path detector.

## Consequences

- A failed real test cannot be accepted because the job was cheap.
- One-family installs remain productive; the trade-off is that a verified
  artifact at or above the previous score is accepted without an independent
  judge.
- Candidate evaluation is order-independent, and the benchmark's `tests_ran`
  label is measured against the candidate's own tree.
- The correction stream is broader and noisier; source-specific defaults and
  artifact attribution keep it usable.
- `hallucinated_path` remains a documented detector gap rather than a silent
  absence.
