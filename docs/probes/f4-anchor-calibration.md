# F4 — Human-anchor calibration results

**Run:** 2026-10-05 · `make probe-anchor` · judges `gemma4:12b-mlx` (`117d0d84cf2a`) and `granite4.2:3b` (`7a65e6414ad6`) · Ollama 0.35.1 · Apple M2 Max, 32 GB.
**Harness:** the probe's `anchor` subcommand scores the 8 blind `eval/anchor`
cases with a human-eval-like prompt (rate 1–5, use the whole range, JSON out)
and reports per-judge call success rate and Spearman rank agreement against
`eval/anchor/ratings.csv`.
**Floors:** success ≥ 0.8, agreement ≥ 0.6 (`execution.policy.min_anchor_agreement`).

## Result

| judge | ok | success rate | Spearman | scores |
|---|---|---|---|---|
| `gemma4:12b-mlx` | 8/8 | **100%** | **0.00** | every case `5` |
| `granite4.2:3b` | 5/8 | **62%** | **0.00** | five cases `5`; three empty responses |

Raw per-case scores (`results/anchor/anchor.json`):

| case | gemma | granite |
|---|---|---|
| md-d1 | 5 | 5 |
| md-d2 | 5 | *(error: empty)* |
| md-d3 | 5 | *(error: empty)* |
| md-s1 | 5 | *(error: empty)* |
| sa-d1 | 5 | 5 |
| sa-d2 | 5 | 5 |
| sa-d3 | 5 | 5 |
| sa-s1 | 5 | 5 |

Anchor ratings for those cases span 2–5 (see `ratings.csv`), so a judge that
returns a constant `5` is not ordering them at all. Spearman is 0 because the
judge series is **constant** (undefined correlation), which is the correct
mathematical encoding of "saturated" — not a bug in the statistic.

## Interpretation

1. **Reliability is repaired for gemma, not for granite.** gemma went from
   22% errors (M3-T7) to 0%; granite is still 38% empty responses even with
   the F4-T0b retry (drop JSON format on alternate attempts).
2. **Saturation persists — now proven on the human anchor.** Both judges put
   nearly every artifact at the ceiling. This confirms the M3-T7 finding
   independently of the probe packets: a reliable judge is not a
   discriminating one.
3. **The LLM judge fails the calibration gate.** 0.00 < 0.6 for both judges,
   and granite additionally fails the 0.8 reliability floor. The probe exits
   non-zero, as designed.
4. **`min_judge_confidence` is confirmed non-discriminative.** The anchor
   result and the earlier ≈0.99 constant are the empirical basis for
   [ADR-0046](../adr/0046-judge-protocol.md) §5: the load-bearing acceptance
   safety is deterministic verification ([ADR-0045](../adr/0045-verification-first-selection.md)),
   not the confidence threshold.

## Gate G-F4 impact

- **"The instrument is trustworthy … agreement with the human anchor above a
  threshold"** — **NOT met** by the LLM judge. The deterministic instrument
  (real code tests, structural checks) is repaired; the *learned-style LLM
  judge* is not.
- **"Deterministic verifiers (cross-family) decide the majority of code
  accepts"** — implemented and unit-tested (ADR-0045); not measured by this
  anchor pass (it needs a code run).
- The honest position: F4 makes the system **not depend** on a signal the
  probe shows is broken. It does not make the broken signal good.

## Follow-ups

- Judge discrimination needs **harder tasks** (the anchor's spread is small)
  and/or a **pairwise** protocol (compare two artifacts) rather than
  absolute 1–5 scoring; absolute scoring saturates.
- granite's empty-response rate on this prompt is a reliability defect worth
  a dedicated retry/format experiment.
- A future distilled verifier is calibrated by this same anchor math
  ([ADR-0046](../adr/0046-judge-protocol.md)).
