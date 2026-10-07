# G-F4 closeout — 10-model probe results

**Run:** 2026-10-06 · Ollama 0.35.1 (dedicated single-resident server,
`OLLAMA_MAX_LOADED_MODELS=1`) · Apple M2 Max, 32 GB, AC power.
**Protocol:** [M3-T7 quality probe](m3-t7-quality-probe.md) + F4 follow-ups
([f4-verifier-run](f4-verifier-run.md)); plan
[gf4-probe-plan](gf4-probe-plan.md).
**Artifacts:** `spikes/m3-t7-probe/results/` (gitignored) — `anchor10/`
(P1), `gf4/` (P2 + P3), `gf4-drift/` (drift check).

## Model roster (10 models / 5 families)

| model | size | family |
|---|---|---|
| `granite4.2:3b` | 3 B | granite |
| `gemma4:e4b-mlx` | e4 B | gemma |
| `granite4.2:8b` | 8 B | granite |
| `ornith-1.5:9b` | 9 B | qwen35 |
| `gemma4:12b-mlx` | 12 B | gemma |
| `qwen3.8:27b-mlx` | 27 B | qwen3.8 |
| `granite4.2:30b` | 30 B | granite |
| `nemotron-3.5-lightning:30b-mlx` | 30 B | nemotron |
| `muse-glimmer:30b-mlx` | 30 B | muse |
| `ornith-1.5:35b` | 35 B | qwen35 |

`ornith` reports family **`qwen35`**, so the probe declares true families in
`personas.<role>.family` and gives each generator a cross-family in-engine
judge.

## P1 — anchor calibration (all 10 judges, `think:false`)

Reliability **10/10 = 100%**; Spearman agreement vs the (agent-provisional)
`eval/anchor` ratings:

| judge | Spearman | Kendall |
|---|---|---|
| `granite4.2:3b` | **0.81** | 0.64 |
| `ornith-1.5:35b` | 0.80 | 0.61 |
| `ornith-1.5:9b` | 0.77 | 0.59 |
| `nemotron-3.5-lightning:30b` | 0.74 | 0.58 |
| `gemma4:12b-mlx` | 0.69 | 0.47 |
| `muse-glimmer:30b` | 0.68 | 0.51 |
| `granite4.2:8b` | 0.67 | 0.49 |
| `qwen3.8:27b-mlx` | 0.67 | 0.51 |
| `granite4.2:30b` | 0.59 | 0.44 |
| `gemma4:e4b-mlx` | 0.57 | 0.43 |

**Agreement is not monotone in size or family** — the 3 B granite beats the
30 B granite, and the 12 B gemma beats the e4 B gemma. 8/10 clear the 0.60
floor. With `think:false` all judges are reliable (granite's empty-response
rate disappears), unlike the earlier default-thinking run.

## P2 — headline: dialectical vs single-shot (148 jobs)

Per (model, goal), engine `score` of the accepted artifact. Full table:
`results/gf4/report.md`. Summary:

- **Code goals — ceiling.** Nearly every model scores **1.00 in both arms**;
  real per-goal tests pass either way. The loop adds nothing on bounded code
  tasks.
- **Text/document — task-dependent.** The loop helps where criteria are
  harder or models weaker: `qwen27b` essay **+1.00**, sunrise **+1.00**,
  email **+0.50**; `gemma12b` email **+1.00**; `nemotron30b` md2html
  **+0.50**; `granite3b`/`muse30b` essay **+0.50**; `granite8b` essay
  **+0.20**. The recurring `local-first-essay` failure is the task's own
  ambiguity (a separate conclusion is a fourth block).
- **Strong models saturate.** `granite4.2:30b` and `ornith-1.5:35b` score
  1.00 on both arms across all 10 goals — no headroom to show a delta.

**T-a — diversity (dialectical / single):** `nemotron30b` 0.74, `qwen27b`
0.72, `ornith9b` 0.68, `granite3b` 0.65, `gemmae4b` 0.65, `granite8b` 0.64,
`ornith35b` 0.60, `granite30b` 0.57, `gemma12b` 0.50, `muse30b` 0.45; single
0.00 by construction. Divergence is real.

**T-b — calibration:** every comparison confidence is in **[0.9, 1.0)** with
mean observed 1.00 — the confidence signal is **degenerate/saturated**,
reconfirming the M3-T7 finding.

**T-c — verdict stability (core `ornith9b`, 3 runs × 10 goals):** **4
"3-same", 6 "2-1", 0 "3-way"** → **40% 3-same**, below the protocol's 70%
threshold. The winner is not reproducible across fresh runs.

**F4 — deterministic acceptance:** **42 code accepts, 42/42 (100%) decided
without the LLM judge.** The verifier carries acceptance.

## P3 — independent judge panel

Judge-outer scoring (each model loaded once). Reliability: `ornith35b`,
`gemma12b`, `granite8b`, `qwen27b` = **100%** (`muse30b` 89% in pass 1; all
100% in the core pass).

**Judge strictness (mean score) varies enormously:** `gemma12b` 0.99,
`ornith35b` 0.98, `granite8b` 0.93, `qwen27b` 0.93, **`muse-glimmer` 0.45**.
The judges are reliable but **not interchangeable** — `muse-glimmer` uses a
different scale, which is precisely the "calibrate before trusting a judge"
problem (ADR-0049 model onboarding).

**Mean panel score by model / arm:**

| model | dialectical | single |
|---|---|---|
| `granite30b` | 0.89 | 0.80 |
| `muse30b` | 0.94 | 0.89 |
| `granite8b` | 0.83 | 0.88 |
| `granite3b` | 0.81 | 0.91 |
| `gemma12b` | 0.85 | 0.99 |
| `gemmae4b` | 0.84 | 0.89 |
| `ornith35b` | 0.83 | 0.82 |
| `ornith9b` | 0.85 | 0.89 |
| `nemotron30b` | 0.93 | 1.00 |
| `qwen27b` | 0.60 | *(single artifacts mostly failed)* |

The independent panel broadly agrees with the engine: **no consistent
dialectical advantage on this bounded task set**, with a few per-model
exceptions.

## Drift check

`ornith9b` re-run at the end of the panel (panel goals, runs=1) scored 1.00
on both arms for all six goals — identical to the main run. No detectable
system drift (the run was ~4 h, not the 28 h cap).

## Confound: `qwen.27b` empty outputs

`qwen3.8:27b-mlx` was the only generator with engine-level phase failures (2
of 24 jobs): one `diverging` call returned Ollama `500 {"error":"EOF"}`, and
one candidate was **empty** (`execute_code: code is required`). The cause is
its **thinking mode** (default `medium`): `think:false` is honored by the
boolean-thinking models but interacts poorly with qwen's level-based
interface, spending the output budget on hidden reasoning and emitting empty
visible content. Its judge scores are correspondingly low (dialectical 0.60).
A targeted re-run with explicit per-model thinking handling is a follow-up.

## Agent (frontier) rating

As an upper-bound *machine* rater, **DeepSeek V4.1 Flash in OpenCode** rated
the same anchor set ([agent rating](../../eval/anchor/agent-rating-deepseek-v4.1-flash.md)):
**Spearman 0.984 / Kendall τ-b 0.970** against the existing ratings (code
cases 1.000), 15/16 identical — far above the local judges' 0.57–0.81. It
confirms the machine-provisional tier is self-consistent, but it is machine vs
machine and so does **not** establish human agreement.

## Gate G-F4 verdict

| arm | status |
|---|---|
| Real code tests (F4-T0) | ✅ |
| Deterministic verifiers decide the majority of code accepts | ✅ **42/42 (100%)**, floor 50% |
| Diversity enforced (F4-T5) | ✅ 0.45–0.74 |
| Judgment bounded (F4-T8) | ✅ (tests; every call capped) |
| Compute scales with difficulty (F4-T2) | ✅ (tests) |
| Reliable judges | ✅ 10/10 at 100% (anchor) |
| Non-degenerate judge scores / human-anchor agreement | ⚠️ **partial** — anchor agreement 0.57–0.81 (8/10 ≥ 0.60); confidence saturated (T-b); judge scales vary widely (muse 0.45) |
| Human anchor | ⏳ **pending** — `eval/anchor` ratings are still agent-provisional (human rating is P4) |

**Conclusion:** the gate is **substantially met and its load-bearing arm is
emphatically met** — deterministic verification decides acceptance and the
loop's winner does not (100% of code accepts decided without the LLM judge).
The **instrument-trust** arm remains partial: the LLM judge is reliable but
weakly discriminating and inconsistently scaled, so it stays an advisory
tiebreaker (ADR-0045/0046), and the human anchor rating must be completed
before the agreement number is final. The headline confirms the central
finding: **on bounded tasks the dialectical loop does not beat single-shot;
its value, where any, is on harder/ambiguous tasks.**

## Caveats

- `eval/anchor` ratings are agent-provisional; P4 (human rating) is pending.
- T-c across fresh jobs confounds model nondeterminism with input
  nondeterminism.
- n is small (10 goals; 16 anchor cases) — report effect sizes, not p-values.
- The engine `score` is the rubric score of the accepted artifact; the
  independent panel is the cross-check.
- 2 of `qwen27b`'s 24 jobs failed on the thinking-mode issue.
