# ADR 0111 — Pairwise judge with position-bias control

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §13.1, §19.3; ADR-0046 (judge protocol), ADR-0063 (F4
closeout); `internal/engine` (`pairwise.go`, `compare.go`)

## Context

The comparison judge is a pointwise prompt: it sees the new artifact and a
summary of the previous one, then returns `winner: new|previous|none`. The F4
closeout found the judge weak and its confidence saturated. Pointwise prompts
are also vulnerable to **position and anchoring effects**: when the judge is
shown one artifact, "new" is an easy default, and when shown two, the first
one often wins. The earlier F4 probe measured pairwise position inconsistency
at 27/40 on the anchor set.

The engine already uses deterministic verifiers first; the judge matters on
open-ended work (documents, plans, research) where no parser can decide. That
is exactly where position bias is most damaging.

## Decision

Add an **opt-in pairwise judge with a position-bias control**.

- **Both orders.** The same judge is called twice for the same pair:
  `A=new, B=previous`, then `A=previous, B=new`. Each call returns
  `{"winner":"A|B|tie","confidence":...,"reasons":[...]}`.
- **Agreement or fallback.** The two position verdicts are mapped back to
  artifact roles. If they agree, the result decides (confidence 1, with both
  reason sets). If they disagree, the engine audits
  `judge_position_bias` and falls back to the existing pointwise judge. A tie
  maps to `previous` (conservative: a tie cannot accept new work).
- **No weaker guard.** The result still passes through the §19.3
  `DecideWinner` guard and the reward-hacking verifier override. Pairwise
  changes how the judge is asked, not what evidence is required.
- **Opt-in.** `execution.policy.pairwise_judge` defaults to `false` because it
  doubles judge calls. Operators who care about judge reliability on
  open-ended work enable it, or a later benchmark can make it the default for
  non-code capabilities.
- **Bounded inputs.** Each artifact is truncated to the existing 4 KiB
  comparison limit; the pairwise prompt names the artifacts as A/B, never as
  "new/previous", so the judge cannot lean on the role name.

## Consequences

- A judge with a stable position preference cannot decide; its inconsistency is
  recorded instead of silently producing a verdict.
- A judge that genuinely compares substance gets a more reliable signal at 2×
  the comparison cost.
- The pattern is the template for a future panel: run N judges in both orders,
  require agreement, and use the human anchor as the calibration. This ADR
  ships the single-judge, two-order control first because it needs no new
  model or storage.

## Alternatives rejected

- **Always use pairwise and remove the pointwise path.** It doubles cost for
  every job and removes the fallback when a model cannot follow the A/B
  format. Opt-in keeps the default stable.
- **Randomize the order once.** A single random order reduces but does not
  detect bias; both orders make the test explicit.
- **Trust the judge's self-reported confidence.** It is saturated (F4
  closeout); agreement across orders is a behavioral signal, not a
  self-report.
