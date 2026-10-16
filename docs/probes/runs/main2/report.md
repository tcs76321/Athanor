# M3-T7 results

Collected 40 job rows.

## Headline — dialectical vs single-shot

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| always-reverse | code | 1.00 | 1.00 | +0.00 | 16037 | 20170 |
| book-collection | code | 1.00 | 1.00 | +0.00 | 4272 | 13162 |
| cache-class | code | 1.00 | 1.00 | +0.00 | 8106 | 14966 |
| fibonacci | code | 1.00 | 1.00 | +0.00 | 6691 | 12991 |
| local-first-essay | text | 0.97 | 0.95 | -0.02 | 5301 | 10047 |
| md2html-readme | document | 1.00 | 1.00 | +0.00 | 7623 | 8459 |
| onboarding-email | text | 0.92 | 0.96 | +0.04 | 3873 | 8638 |
| stringutils | code | 1.00 | 1.00 | +0.00 | 4539 | 14224 |
| sunrise-alarm-brief | document | 0.92 | 1.00 | +0.08 | 9151 | 10839 |
| todo-list | code | 1.00 | 1.00 | +0.00 | 4910 | 12512 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 30 | 0.99 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| 3-same | 7 |
| 2-1 | 3 |

## T-a — candidate diversity

Mean pairwise Jaccard distance (dialectical): 0.795
Mean pairwise Jaccard distance (single): 0.000

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
