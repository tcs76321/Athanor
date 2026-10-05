# ADR 0043 — First-artifact acceptance does not require better_than_previous

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §19.3; ADR-0014; ROADMAP M3; `docs/probes/m3-t7-quality-probe.md`

## Context

The §19.3 deterministic guard (`DecideWinner`) honors the judge's `winner: "new"`
verdict only when at least one `EvaluationRecord` backs it, defined originally
as `better_than_previous == true AND confidence > min_judge_confidence`.

The M3-T7 smoke run exposed a defect in that rule for a project's **first
artifact**. The comparison judge returned `winner: "new"` with sound reasons
("no previous artifact, so the new artifact is accepted as the baseline"), and
all three candidates had `passed_tests = true`, empty `missing_criteria`, and
`confidence` 0.90–0.95 — yet `DecideWinner` downgraded the verdict to `"none"`
and failed the job, because every record carried `better_than_previous = false`.

`better_than_previous` is meaningless when there is no previous artifact, and
real models set it inconsistently: across four fresh-project jobs in the same
smoke, two records came back `true` (accepted) and two `false` (failed), at
identical quality. The probe creates one fresh project per job, so **every
probe job was a coin flip**, and production first-runs are equally affected.

The §19.3 rule as written is "tests pass better-or-equal AND criteria improve-
or-equal AND no new security issue AND confidence > threshold." With no prior
artifact, "better or equal" is trivially satisfied; the engine's original
encoding of that clause via `better_than_previous` was simply wrong for the
empty-comparison case.

## Decision

In `DecideWinner`, when `hasPrevious == false`, a record backs `"new"` if it
**passed evaluation** (`PassedTests == true`) — or was explicitly marked
`better_than_previous` — **and** `confidence > threshold`. When a previous
artifact exists, the original `better_than_previous` requirement is unchanged.

The change is confined to the no-previous branch:

```go
backed := r.BetterThanPrevious
if !hasPrevious {
    backed = r.BetterThanPrevious || r.PassedTests
}
if backed && r.Confidence > threshold { strongNew = true }
```

The guard remains a floor on acceptance, not a recommendation engine: a judge
that says `"previous"` or `"none"` is still never upgraded. A no-previous
record that neither passed nor was marked better is still downgraded to
`"none"`. The threshold check is still strict (`>`).

## Consequences

- A project's first artifact is accepted whenever a candidate passed
  evaluation with sufficient judge confidence — deterministically, independent
  of an under-specified boolean the model guesses at.
- The M3-T7 probe measures artifact quality instead of whether the model
  happened to set `better_than_previous`; the dialectical-vs-single comparison
  becomes meaningful.
- Subsequent artifacts (with a prior accepted baseline) are unaffected: the
  `better_than_previous` requirement still governs whether a new candidate
  supersedes the incumbent.
- Tests: two new `DecideWinner` rows pin the passing/no-previous acceptance and
  the failing/no-previous downgrade; all prior rows are unchanged.

## Alternatives rejected

- **Prompt the model to set `better_than_previous = true` when there is no
  previous.** Fragile and semantically dishonest; the flag has no referent. The
  engine should not depend on the model's interpretation of an impossible
  comparison.
- **Disable the guard when there is no previous.** Would drop the confidence
  floor too; a low-confidence "new" should still fail. Keeping the confidence
  check preserves the guard's intent.

## Not in scope

- The "LLM says previous/none but a strong record exists" direction (a
  recommendation engine); still deliberately out of the §19.3 contract.
- Multi-instance consensus for judgment; that is a separate M3-T7 follow-up.
