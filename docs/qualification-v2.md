# Qualification v2 — a ten-minute first signal

**Status:** implemented · **Date:** 2026-11-06 ·
**Refs:** [ADR-0049](adr/0049-model-onboarding-qualification.md),
[ADR-0104](adr/0104-composition-space-not-personas.md),
[frozen corpus v2](frozen-corpus-v2.md).

## Objective

Qualification is the **first signal** for a machine, not a replacement for the
frozen benchmark. It has to:

- separate models enough to choose a baseline cast;
- show the evidence behind every score;
- finish in about ten minutes on a normal machine;
- be resumable when it does not;
- leave the full benchmark as the controlled study.

## What was wrong

- 22 cases per model clustered near the same scores (78–79% for most of the
  pool), which made the ranking look arbitrary.
- Only aggregate scores were stored; the operator could not see which case
  failed or what the question was.
- The screen was a flat list. It did not map results to the small/medium/big
  roles the engine actually uses.
- The wall bound was two hours, so a slow model could hold the wizard hostage.

## Design

### 1. A broader, harder default ladder

The default ladder grows from 22 to **41 cases** (19 grounded generators + 22
fixed-label judge pairs), with a deep ladder of **53**. The additions target
state, ordering, slicing, schema shape, concurrency, injection, idempotency,
timeouts, data integrity, document structure, audience, accessibility, secrets,
and test isolation — the failures that separate models once one-line recall
saturates.

Every case is deterministic: exact/JSON for generators, fixed good/bad labels
for judges. No judge model is consulted during qualification.

### 2. Ten minutes, not two hours

- Default: **10 minutes** wall time.
- Deep: **20 minutes**.
- The run is resumable at case granularity; completing a later session skips
  recorded cases.
- Partial results still feed the pool, with the sample count and the wall
  status visible.

The screen is deliberately a **subset and a prior**. The frozen benchmark is the
controlled study and is not edited here.

### 3. Per-case evidence

Each case stores kind, case id, capability, prompt, expected answer, actual
answer, pass/fail, duration, and any error. The Qualification and Models pages
render a per-model `<details>` table, so the operator can inspect the question
and the answer behind every score.

### 4. Role baseline: small / medium / big

The measured pool is sorted by model size into small, medium, and big tiers:

| Role | Tier | Intent |
|---|---|---|
| `wide` | small | fast ingestion, repository reading, summaries |
| `main` | medium | normal execution and synthesis; the first attempt |
| `tall` | big | planning, hard reasoning, difficult design |

Within a tier, the role score is the average of the capability scores that
matter for that role (`general/document/text` for wide, `code/general/data` for
main, `code/judgment/general` for tall). A clearly better tier can override the
default when the score gap is larger than 0.1; otherwise the size tier wins.

The baseline cast is displayed above the ranking. It is a **first signal**:
the composition selector and real job outcomes continue to adapt selection.
The internal default remains: try `main` (medium) first, escalate to `tall`
when the task is hard or retries fail, and use `wide` for ingestion.

### 5. The machine score is not a model score

The Models page shows `host capacity N/100` with a tooltip. It is a
RAM/CPU/disk capacity heuristic (`composition.ScoreMachine`), not a quality
score for a model. The measured ranking below it is the model score. This
removes the "why is my machine 82/100?" ambiguity.

## Full benchmark

The frozen v2 corpus remains **41 deterministic tasks** (18 code, 13 document,
5 text, 5 data) plus 4 memory scripts and 6 human reviews. It is already large
enough for its purpose; running every installed model against it is hours of
work and is not a first-use screen.

The qualification ladder borrows the *shape* of the benchmark — capability
coverage, code/data correctness, document structure, text audience, media
accessibility, injection and secrets — but it is not a second frozen corpus.
The benchmark is the controlled study; qualification is the local prior.

## Remaining work

- Adaptive second stage: run the extended cases only for the top models in each
  tier, using the ten-minute budget for separation rather than breadth.
- Bootstrap confidence intervals on the role scores, so overlapping models are
  visibly overlapping rather than falsely precise.
- A held-out qualifier slice, never shown in the UI, to detect overfitting to
  the screen.
- Align the host-side `athanor models -audit` with the adaptive host/GPU budget
  (ADR-0107).
