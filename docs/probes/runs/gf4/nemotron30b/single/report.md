# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| nemotron30b | nemotron | book-collection | code | 1.00 | 0.00 | -1.00 | 3067 | 0 |
| nemotron30b | nemotron | fibonacci | code | 0.67 | 0.00 | -0.67 | 5413 | 0 |
| nemotron30b | nemotron | local-first-essay | text | 0.50 | 0.00 | -0.50 | 5801 | 0 |
| nemotron30b | nemotron | md2html-readme | document | 0.50 | 0.00 | -0.50 | 4688 | 0 |
| nemotron30b | nemotron | onboarding-email | text | 1.00 | 0.00 | -1.00 | 6169 | 0 |
| nemotron30b | nemotron | sunrise-alarm-brief | document | 1.00 | 0.00 | -1.00 | 4939 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| nemotron30b | 0.000 | 0.000 |

## F4 — verification decisions

Code accepts: 1; decided without the LLM judge: 1 (100%).
Comparisons that called the LLM judge: 2.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
