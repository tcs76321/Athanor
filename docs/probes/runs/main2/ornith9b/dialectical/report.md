# M3-T7 results

Collected 30 job rows.

## Headline — dialectical vs single-shot

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| always-reverse | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| book-collection | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| cache-class | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| fibonacci | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| local-first-essay | text | 0.00 | 0.95 | +0.95 | 0 | 10047 |
| md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 8459 |
| onboarding-email | text | 0.00 | 0.96 | +0.96 | 0 | 8638 |
| stringutils | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 10839 |
| todo-list | code | 0.00 | 0.00 | +0.00 | 0 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 12 | 0.98 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| 3-same | 9 |
| 2-1 | 1 |

## T-a — candidate diversity

Mean pairwise Jaccard distance (dialectical): 0.802
Mean pairwise Jaccard distance (single): 0.000

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
