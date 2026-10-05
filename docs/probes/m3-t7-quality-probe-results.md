# M3-T7 — Results

> Fill this in after the full run. Skeleton only until then; the smoke
> findings live in [`m3-t7-quality-probe.md`](m3-t7-quality-probe.md).

## Environment

- Machine / GPU:
- Ollama version:
- Models + digests (`ollama show <model>`): generator `ornith-1.5:9b`; judges `gemma4:12b-mlx`, `granite4.2:3b`
- Config: `inference.think: false`, `max_output_tokens: 4096`, `json_format: true`, `json_schema: false`
- Reflection: on / off (`-no-reflect`)
- Run wall time, `soak.csv` summary (peak RSS, max DB size, min free disk):

## Headline — dialectical (N=3) vs single (N=1)

Per goal: mean score over runs, mean token cost, delta. Cost-normalized
(score per 1k tokens) matters as much as raw score.

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| | | | | | | |

Summary: how often did the dialectical arm beat/equal/lose, and at what cost?

## T-a — candidate diversity (per cycle)

From the engine's `divergence_jaccard` events (per-cycle; the single arm is 0
by construction).

| arm | mean diversity | n |
|---|---|---|
| dialectical | | |
| single | | |

## T-b — judge-confidence calibration (dialectical arm)

Reliability diagram: bin confidence, mean observed score. Perfect calibration
is `mean == midpoint`.

| confidence bin | n | mean observed score |
|---|---|---|
| [0.5,0.6) | | |
| [0.6,0.7) | | |
| [0.7,0.8) | | |
| [0.8,0.9) | | |
| [0.9,1.0) | | |

Trigger: >20% of bins more than 0.2 off the diagonal ⇒ revisit
`min_judge_confidence`.

## T-c — verdict stability at T=0 (dialectical arm)

Winners across each goal's three runs.

| class | goals |
|---|---|
| 3-same | |
| 2-1 | |
| 3-way | |

Confound: fresh jobs confound model nondeterminism with input nondeterminism
(note it). The seeded/unseeded comparison (`-seed derived`) isolates backend
residual if run.

## Third-party judge scores

From `judge` → `judges.json`; per judge, mean score per goal and arm.

| goal | arm | gemma4:12b-mlx | granite4.2:3b |
|---|---|---|---|
| | | | |

## Human rating sheet

Rate each artifact against its stated criteria on a 1–5 scale. The runner's
`packets/index.json` maps `packet_id` → goal/model/arm/run; packets are blind.
One row per artifact (dialectical runs 1–3 and the single run).

| packet_id | goal | arm | run | criteria met (Y/N) | score 1–5 | notes |
|---|---|---|---|---|---|---|
| | | | | | | |

## Findings & decision

- Did the loop earn its cost? Where?
- Judge quality / calibration issues.
- Follow-ups: which F4 tasks ([plan](../f4-plan.md)) does this justify, and in
  what order? If `min_judge_confidence` is mis-calibrated, open an ADR.
