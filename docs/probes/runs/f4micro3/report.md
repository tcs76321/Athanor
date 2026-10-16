# M3-T7 results

Collected 12 job rows.

## Headline — dialectical vs single-shot

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| local-first-essay | text | 0.00 | 0.33 | +0.33 | 6431 | 14093 |
| md2html-readme | document | 1.00 | 1.00 | +0.00 | 4133 | 13114 |
| todo-list | code | 1.00 | 1.00 | +0.00 | 4519 | 9776 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 7 | 1.00 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| 3-same | 2 |
| 2-1 | 1 |

## T-a — candidate diversity

Mean pairwise Jaccard distance (dialectical): 0.774
Mean pairwise Jaccard distance (single): 0.000

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
