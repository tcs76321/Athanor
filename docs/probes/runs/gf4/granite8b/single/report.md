# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| granite8b | granite | book-collection | code | 1.00 | 0.00 | -1.00 | 3512 | 0 |
| granite8b | granite | fibonacci | code | 1.00 | 0.00 | -1.00 | 6931 | 0 |
| granite8b | granite | local-first-essay | text | 0.80 | 0.00 | -0.80 | 7920 | 0 |
| granite8b | granite | md2html-readme | document | 1.00 | 0.00 | -1.00 | 4629 | 0 |
| granite8b | granite | onboarding-email | text | 1.00 | 0.00 | -1.00 | 9162 | 0 |
| granite8b | granite | sunrise-alarm-brief | document | 1.00 | 0.00 | -1.00 | 7063 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| granite8b | 0.000 | 0.000 |

## F4 — verification decisions

Code accepts: 1; decided without the LLM judge: 1 (100%).
Comparisons that called the LLM judge: 3.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
