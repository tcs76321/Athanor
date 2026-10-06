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

_Pending: `anchor -protocol {pointwise5, pointwise100, pairwise}` on the
16-case anchor. Results appended here._
