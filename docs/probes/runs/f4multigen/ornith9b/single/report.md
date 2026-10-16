# M3-T7 results

Collected 3 job rows.

## Headline — dialectical vs single-shot

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| local-first-essay | text | 1.00 | 0.00 | -1.00 | 7043 | 0 |
| md2html-readme | document | 1.00 | 0.00 | -1.00 | 3310 | 0 |
| todo-list | code | 1.00 | 0.00 | -1.00 | 3396 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity

Mean pairwise Jaccard distance (dialectical): 0.000
Mean pairwise Jaccard distance (single): 0.000

## F4 — verification decisions

Code accepts: 1; decided without the LLM judge: 1 (100%).
Comparisons that called the LLM judge: 2.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
