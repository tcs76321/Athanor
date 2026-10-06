# F4 follow-up — verifier-mode and multi-model runs

Runs 2026-10-05 after the F4 follow-ups (fence-free persistence, single-model
residency, cross-family judge). Environment: Apple M2 Max, 32 GB; Ollama
0.35.1 on a dedicated single-resident instance (`OLLAMA_MAX_LOADED_MODELS=1`,
`OLLAMA_NUM_PARALLEL=1`, port 11435); generator `ornith-1.5:9b`; judge
`gemma4:12b-mlx` (cross-family). Micro set: `local-first-essay`,
`md2html-readme`, `todo-list`.

Both runs use `execution.policy.judge_mode: verifier`. B-run2 is identical
except `personas.alternative.model` = gemma4 (multi-model generation), so the
only variable between the two is the generation source.

## B-run1 — verifier mode, single-model generation (`results/f4verifier`)

| goal | dialectical | single |
|---|---|---|
| local-first-essay | completed, failed, completed (2/3) | completed |
| md2html-readme | completed, failed, failed (1/3) | completed |
| todo-list | completed, completed, failed (2/3) | completed |

Diversity (dialectical): 0.61–0.83. Zero orphan pods.

**Deterministic accept fraction: 3/3 code accepts (100%) decided without the
LLM judge** (`judge_called=false`). Comparisons that called the judge: 8.
`cross_family_ok=true` on the judge path (generator `ornith-1.5` vs judge
`gemma4`).

## B-run2 — verifier mode, multi-model generation (`results/f4multigen`)

`alternative` = gemma4, so candidates alternate `ornith` (main) and `gemma4`
(alternative).

| goal | dialectical | single |
|---|---|---|
| local-first-essay | failed, completed, failed (1/3) | failed |
| md2html-readme | completed 3/3 | completed |
| todo-list | completed 3/3 | completed |

Diversity (dialectical): 0.61–0.88. **Deterministic accept fraction: 4/4 code
accepts (100%)**. `cross_family_ok=true`.

## Findings

1. **The Gate G-F4 verifier arm is met.** Across both runs, **7/7 code accepts
   were decided by the deterministic test verifier** (`judge_called=false`),
   far above the configured 50% floor. A failed test is likewise a
   deterministic reject. This is the deliverable the gate asked for.
2. **Cross-family judging is live and enforced.** The comparison judge was
   `gemma4` (family ≠ `ornith-1.5`), `cross_family_ok=true`, and the judge was
   only called when no hard verifier decided (text/document, and code ties).
3. **Multi-model generation did not raise textual diversity.** The mean
   pairwise Jaccard was already ~0.6–0.88 with one model (F4-T5's divergence
   seeds do the work); swapping in gemma for the `alternative` role moved it
   little. It did correlate with a higher `md2html`/`todo-list` accept rate,
   but n=3 — suggestive, not established.
4. **Most non-code failures are the judge declining on literal criteria, not
   machinery failure.** The recurring `local-first-essay` failure is the
   task's own ambiguity: "exactly three paragraphs … a one-sentence
   conclusion" — a separate conclusion is a **fourth** block, so the
   cross-family judge correctly rejects it (`winner=none`, confidence 1). The
   anchor encodes the same distinction (`essay-e1` folds the conclusion into
   the third paragraph; `essay-e2` fails).
5. **Single vs dialectical is still unresolved.** Both arms accept similar
   artifacts; the failures are criteria-driven, and n is tiny. The headline
   experiment needs the harder task set (started in C0) to separate them.

## C1 — judge protocol discrimination

On the **expanded 16-case anchor** (C0), with objective labels on the four
code cases:

| protocol | judge | reliability | Spearman | Kendall |
|---|---|---|---|---|
| pointwise5 | gemma4:12b-mlx | 94% | **0.61** | 0.46 |
| pointwise5 | granite4.2:3b | 81% | 0.53 | 0.38 |
| pointwise100 | gemma4:12b-mlx | 94% | **0.62** | 0.47 |
| pointwise100 | granite4.2:3b | 56% | 0.78 | 0.62 |
| pairwise (40 pairs × 2 orders) | gemma4:12b-mlx | 70% | — | — |
| pointwise5 (control) | **ornith-1.5:9b** (the generator, `think:false`) | **100%** | **0.77** | 0.59 |

**Granularity did not help.** The 0–100 scale left gemma essentially
unchanged (0.62 vs 0.61) and made granite *less* reliable (81% → 56%), though
granite's agreement among its answers rose (0.53 → 0.78, n=9 — noisy). The
TrustJudge "finer granularity" prediction did not transfer to these local
models, which suggests they are not spreading across the scale regardless of
its range.

**Pairwise is the worst protocol here — position bias dominates.** Of the 40
capped pairs (each asked in both orders): **27 were inconsistent** (the winner
flipped with presentation order), 12 errored (empty response), and only **1 was
decisive**. The reported pair-accuracy of 1.00 therefore rests on a single
pair and is not meaningful. Absolute pointwise scoring, for all its weakness,
at least yields a monotone-ish signal (Spearman ≈ 0.61); pairwise on a 7.6 GB
local model does not.

**Conclusion for C1:** none of the three protocols yields a *strong*
discriminator. The best is pointwise-5 with gemma (Spearman ≈ 0.61, 94%
reliable). This is the empirical basis for keeping deterministic verifiers as
the load-bearing acceptance signal and treating the LLM judge as an advisory
tiebreaker (ADR-0045/0046), rather than changing the engine's judge protocol
(C3 is **not** triggered).

### The same-model control: the generator is the best judge on this anchor

Running the **generator itself** (`ornith-1.5:9b`, `think:false`) as the judge:

| judge | family | reliability | Spearman | Kendall |
|---|---|---|---|---|
| ornith-1.5:9b | *same as generator* | 100% | **0.77** | 0.59 |
| gemma4:12b-mlx | different | 94% | 0.61 | 0.46 |
| granite4.2:3b | different | 81% | 0.53 | 0.38 |

**The same-model judge agrees with the human anchor best.** This is an
uncomfortable result for a naive reading of "cross-family is better," and it
needs a precise interpretation:

- The anchor measures **absolute scoring agreement** — "when this judge rates
  an artifact, does it order artifacts like the human does?" On that
  property, capability appears to dominate family: ornith is the strongest
  model in the set (per its own card, above Gemma-4-31B on SWE-bench/Terminal-
  Bench) and it scores best, even judging its own outputs.
- It does **not** measure the property F4-T3 actually guards against:
  **correlated error across a candidate set** (the probe's "verifier is the
  generator" collapse). A judge can be well-calibrated in absolute terms and
  still fail to rank a generator's own near-identical candidates, because its
  blind spots move with the generator's.
- With n=16 and agent ratings, 0.77 vs 0.61 is **suggestive, not
  established**; and the two judges were run under slightly different settings
  (gemma default `think`, ornith `think:false`).

**Revised position:** the cross-family rule (ADR-0045) remains a sensible
*default guardrail* against the degenerate same-weights self-judge, but this
measured anchor does **not** justify claiming cross-family is more accurate.
The defensible claim is narrower and stronger: *deterministic verification
decides code objective accepts; the LLM judge — same family or not — is a
weak signal and belongs only in the tie/fallback path.* A paired experiment
(same candidate set judged by generator vs a different model, measuring
candidate-ranking agreement, not anchor scoring) is the right next test.

**Key correction:** the earlier 0.00 agreement was an artifact of the
underpowered 8-case anchor (ratings 2–5, near-ceiling). With clear low and
high anchors — especially the objective code cases (rating 1 vs 5) — gemma
reaches Spearman 0.61 and clears the 0.60 floor; granite (0.53) does not.
The judge layer is not hopeless; the anchor was.

