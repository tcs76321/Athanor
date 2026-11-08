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
Models page shows these as their own columns, and the starting roster uses them
per function: the worker weighs overall quality, speed, context, and cost; the
architect weighs hard reasoning and depth; the scout weighs speed, cost, and
summarization; the screener weighs security, judgment, and instruction.

Every case is deterministic: exact/JSON/contains+word-bound checks for
generators, fixed good/bad labels for judges. No judge model is consulted during
qualification. A qualification pass evicts each screened model (`keep_alive: 0`)
after measuring it, so the last model is not left resident.

### 2. Open-ended, balanced, and resumable

The run has three depths:

- **Minimal**: two cases per capability for every model (32 cases per model on
  the current ladder), always including a judge pair. This is the smallest
  screen that still covers every capability more than once.
- **Balanced**: every case in the standard 80-case ladder, about two and a half
  times the minimum. This is the recommended screen to start with: broad
  capability coverage without the long deep cases.
- **Complete**: every case in the 119-case deep ladder, including the deep
  long-context, repair, and adversarial additions.

Each depth shows its own time estimate, and changing the depth while the
screen runs is applied between cases, so selecting Balanced or Complete does
not wait for the minimum to finish.

- **Starts when chosen**: step 4 shows the three depths and their estimates;
  nothing new runs until the operator picks one. A background poll or a stale
  tab never starts a new run. While it runs, the page shows case runs done,
  elapsed time, the model being measured, and an estimate of the time left.

The state is resumable at case granularity. A Core restart clears the stale
running flag, and opening the qualification step resumes the depth the operator
already chose from the completed cases. The progress panel names the model
being measured, so a long load is visible rather than silent. While any job can
call Ollama, the screen waits instead of evicting the job's model. Scores are
computed only on the
**intersection** of cases completed by every model, so an interrupted block
never advantages the models that happened to run first. A model that cannot be
reached is an infrastructure error: the screen stops with the error and leaves
its remaining cases pending, rather than recording a wrong answer that would
permanently poison the score. The operator can fix the backend and resume.

The screen is deliberately a **subset and a prior**. The frozen benchmark is the
controlled study and is not edited here.

### 3. Per-case evidence

Each case stores kind, case id, capability, prompt, expected answer, actual
answer, pass/fail, duration, and any error. The Qualification and Models pages
render a per-model `<details>` table, so the operator can inspect the question
and the answer behind every score.

### 4. Functional roster and capability trust

The starting cast is not a size ladder. It is the five functions Athanor
actually uses: **Worker** (main), **Architect** (tall), **Scout** (wide),
**Screener** (security), and **Second opinion** (alternative). The same builder
feeds the roster UI and the persona assignments.

Selection uses one evidence vector per model: measured quality, a
sample-weighted Wilson lower bound shown as the trust floor, working context,
decode speed, resident cost, and family independence.
Role rules:

- **Architect** — best trusted quality and hard-reasoning coverage; size is
  allowed. A one-displayed-point quality band keeps a specialist with missing
  capabilities from outranking the overall leader.
- **Worker** — quality, speed, context, resident cost, and confidence. The
  worker must be at least 35% of the architect's size, and stays within one
  displayed percentage point of the best remaining model (rounded, so 81% can
  compete with 82% when it is meaningfully cheaper, but 80% cannot).
- **Scout** — speed and cost dominate. It must be at most 65% of the worker's
  size, relaxed to 85% only when nothing smaller exists.
- **Screener** — security, judgment, instruction, and confidence, from a family
  outside the generator roles.
- **Second opinion** — a different family, high uncertainty, and complementary
  strengths.

The UI also shows **capability leaders**: the best and runner-up model for every
capability, with sample counts and gap. Below that, **next experiments** lists
models with the most useful unexplored evidence. The engine keeps its automatic
exploration budget (10% on familiar work, more on novel work); security, HITL,
and irreversible work never explore.

Qualification records Ollama's decode counters and stores tokens/sec. Models
measured before this change show speed as unmeasured until the next screen.
Blank or architecture-only catalog families are replaced by the model-name
base for lineage checks (`qwen3.8:27b-mlx` → `qwen3.8`), with the reported
family as a fallback.

The measured ranking still shows every model's overall and capability scores.
The roster and leaders are derived from that same evidence and adapt as real job
outcomes arrive.

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
