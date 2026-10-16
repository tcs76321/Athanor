# M3-T7 results

Collected 10 job rows.

## Headline — dialectical vs single-shot

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| always-reverse | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| book-collection | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| cache-class | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| fibonacci | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| local-first-essay | text | 0.97 | 0.00 | -0.97 | 5301 | 0 |
| md2html-readme | document | 1.00 | 0.00 | -1.00 | 7623 | 0 |
| onboarding-email | text | 0.92 | 0.00 | -0.92 | 3873 | 0 |
| stringutils | code | 0.00 | 0.00 | +0.00 | 0 | 0 |
| sunrise-alarm-brief | document | 0.92 | 0.00 | -0.92 | 9151 | 0 |
| todo-list | code | 0.00 | 0.00 | +0.00 | 0 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity

Mean pairwise Jaccard distance (dialectical): 0.000
Mean pairwise Jaccard distance (single): 0.000

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
