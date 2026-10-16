# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| granite3b | granite | book-collection | code | 1.00 | 0.00 | -1.00 | 3608 | 0 |
| granite3b | granite | fibonacci | code | 1.00 | 0.00 | -1.00 | 3663 | 0 |
| granite3b | granite | local-first-essay | text | 0.00 | 0.00 | +0.00 | 4957 | 0 |
| granite3b | granite | md2html-readme | document | 1.00 | 0.00 | -1.00 | 7640 | 0 |
| granite3b | granite | onboarding-email | text | 0.80 | 0.00 | -0.80 | 5535 | 0 |
| granite3b | granite | sunrise-alarm-brief | document | 1.00 | 0.00 | -1.00 | 3631 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| granite3b | 0.000 | 0.000 |

## F4 — verification decisions

Code accepts: 1; decided without the LLM judge: 1 (100%).
Comparisons that called the LLM judge: 2.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
