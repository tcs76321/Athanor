# Qualification v2 — a category-aware first signal

**Status:** implemented · **Date:** 2026-11-06 ·
**Refs:** [ADR-0049](adr/0049-model-onboarding-qualification.md),
[ADR-0104](adr/0104-composition-space-not-personas.md),
[frozen corpus v2](frozen-corpus-v2.md).

## Objective

Qualification is the **first signal** for a machine, not a replacement for the
frozen benchmark. It has to:

- separate models enough to choose a baseline cast;
- show the evidence behind every score;
- finish in a bounded window on a normal machine;
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

The default ladder is **80 cases** (40 grounded generators + 40 fixed-label
judge pairs), with a deep ladder of **119**. The additions target
state, ordering, slicing, schema shape, concurrency, injection, idempotency,
timeouts, data integrity, document structure, audience, accessibility, secrets,
and test isolation — the failures that separate models once one-line recall
saturates.

It also scores the **Athanor-internal task families separately**, because they
are different jobs: `summarize` (compaction and exact-fact retention),
`security` (injection, secret handling, unsafe-action classification),
`agentry` (next action, dependency order, budget/defer, clarification),
`instruction` (format and constraint discipline), `grounding` (citations and
unsupported claims), `repair` (minimal fixes and observation), `longctx`
(exact retrieval across distractors), `operation` (engine-executed operation
choice), `planning` (dependencies, parallel steps, infeasibility, replanning),
and `calibration` (unknown/unsupported/conflict instead of fabrication). The
Models page shows these as their own columns, and the role scores use them
(wide: longctx/summarize/grounding; main: repair/operation; tall:
planning/calibration/security).

Every case is deterministic: exact/JSON/contains+word-bound checks for
generators, fixed good/bad labels for judges. No judge model is consulted during
qualification. A qualification pass evicts each screened model (`keep_alive: 0`)
after measuring it, so the last model is not left resident.

### 2. Open-ended, balanced, and resumable

The run has two layers:

- **Core**: one case per capability (general, code, data, document, text,
  media, summarize, security, agentry, instruction, grounding, repair, longctx,
  operation, planning, calibration, judgment) for every model. This is the
  bare minimum and always runs. It has no time cutoff: on a slow machine with a
  large pool it can take a long time, and that is correct.
- **Minimum run time**: the operator chooses 10, 20, 30, 45, or 60 minutes as
  the minimum total qualification time. The core may exceed it (large pool or
  slow machine), and that is correct. If the core finishes early, extra blocks
  fill the remaining time. The first successfully screened model's core pass
  is the cost probe; Athanor estimates per-case cost from it, sizes blocks to
  the remaining time (capped at 30 cases), and may wrap up a final block a few
  minutes past the minimum rather than stop with time unused. Every block runs
  for every model or not at all.

The state is resumable at case granularity. Scores are computed only on the
**intersection** of cases completed by every model, so an interrupted block
never advantages the models that happened to run first. A model that is
unavailable is marked failed for its cases, not silently skipped.

The screen is deliberately a **subset and a prior**. The frozen benchmark is the
controlled study and is not edited here.

### 3. Per-case evidence

Each case stores kind, case id, capability, prompt, expected answer, actual
answer, pass/fail, duration, and any error. The Qualification and Models pages
render a per-model `<details>` table, so the operator can inspect the question
and the answer behind every score.

### 4. Role baseline: smallest top-tier main, genuinely smaller wide

The baseline cast follows the ranking rather than arbitrary size thirds:

- **Tall** is the strongest tall model. It is allowed to be several times
  larger; tall is where a small-context, high-power model earns its resident
  cost.
- **Main** is the smallest model in the highest tier *below tall*. A one-point
  score gain does not justify a much larger resident model, and the tall model
  is not a main candidate. Ties break by smaller size, then score, then name.
- **Wide** must be actually smaller than main (preferring at most 85% of main's
  size), with the best wide score and a different family preferred. If nothing
  is smaller, wide shares main and the UI says to add a smaller model.
- **Security** is the best measured judge from a family outside the generator
  roles; **alternative** is a different family from main/tall/judge when one is
  available.

The ranking shows overall, code, data, judgment, summarization, security, and
agentry, plus family and size. The recommended pool is five or more models
across at least two families and three size bands; the functional floor remains
four. The baseline cast is a **first signal**: the composition selector and real
job outcomes continue to adapt selection.

### 5. The machine score is not a model score

The Models page shows `host capacity N/100` with a tooltip. It is a
RAM/CPU/disk capacity heuristic (`composition.ScoreMachine`), not a quality
score for a model. The measured ranking below it is the model score. This
removes the "why is my machine 82/100?" ambiguity.

## Relationship to the benchmark

There are two layers, and they must not be confused:

1. **Qualification screen** — the cheap, one-shot ladder above. It chooses the
   baseline cast and is a local prior, never a headline benchmark result.
2. **Benchmark anchor** — the real subset of the frozen corpus:
   [`eval/bench/v2/anchor.yaml`](../eval/bench/v2/anchor.yaml), 12 tuning-only H
   tasks, one model per Core restart, one single-shot pass. Run it with
   `make bench-anchor-core` (Core Pod, standard `results.json`/`report.md`,
   standard leaderboard). It is the same instrument as the full v2 run, so its
   results contribute to the leaderboard and are not a separate score.

The screen is **based on** the same work through
[`eval/qualification/categories.yaml`](../eval/qualification/categories.yaml):
each internal category (summarize, security, agentry, instruction, grounding)
names the benchmark/diagnostic tasks that anchor it. The ladder uses the same
deterministic check vocabulary (`exact`, `json`, `contains` plus word bounds)
and the same capability names; a provenance test fails if a category or task id
drifts.

The frozen v2 corpus remains **41 deterministic tasks** plus 4 memory scripts
and 6 human reviews. It is never edited for qualification; newer category tasks
live in the non-frozen [`eval/diagnostic`](../eval/diagnostic/tasks.yaml)
corpus and can be promoted into a future frozen version.

## Remaining work

- Adaptive second stage: run the extended cases only for the top models in each
  tier, using the bounded window for separation rather than breadth.
- Bootstrap confidence intervals on the role scores, so overlapping models are
  visibly overlapping rather than falsely precise.
- A held-out qualifier slice, never shown in the UI, to detect overfitting to
  the screen.
- Align the host-side `athanor models -audit` with the adaptive host/GPU budget
  (ADR-0107).
