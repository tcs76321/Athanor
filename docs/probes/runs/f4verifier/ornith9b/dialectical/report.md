# M3-T7 results

Collected 9 job rows.

## Headline — dialectical vs single-shot

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| local-first-essay | text | 0.00 | 1.00 | +1.00 | 0 | 10461 |
| md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 7582 |
| todo-list | code | 0.00 | 1.00 | +1.00 | 0 | 7306 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 9 | 1.00 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| 2-1 | 3 |

## T-a — candidate diversity

Mean pairwise Jaccard distance (dialectical): 0.756
Mean pairwise Jaccard distance (single): 0.000

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 6.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
