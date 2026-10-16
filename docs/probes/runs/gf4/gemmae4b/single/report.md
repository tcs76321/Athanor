# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| gemmae4b | gemma | book-collection | code | 1.00 | 0.00 | -1.00 | 3239 | 0 |
| gemmae4b | gemma | fibonacci | code | 1.00 | 0.00 | -1.00 | 3950 | 0 |
| gemmae4b | gemma | local-first-essay | text | 0.00 | 0.00 | +0.00 | 5779 | 0 |
| gemmae4b | gemma | md2html-readme | document | 1.00 | 0.00 | -1.00 | 3227 | 0 |
| gemmae4b | gemma | onboarding-email | text | 0.00 | 0.00 | +0.00 | 5714 | 0 |
| gemmae4b | gemma | sunrise-alarm-brief | document | 1.00 | 0.00 | -1.00 | 4756 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| gemmae4b | 0.000 | 0.000 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 2.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
