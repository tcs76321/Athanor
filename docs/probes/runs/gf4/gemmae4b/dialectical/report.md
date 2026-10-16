# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| gemmae4b | gemma | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 6819 |
| gemmae4b | gemma | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 7863 |
| gemmae4b | gemma | local-first-essay | text | 0.00 | 0.00 | +0.00 | 0 | 14888 |
| gemmae4b | gemma | md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 6596 |
| gemmae4b | gemma | onboarding-email | text | 0.00 | 0.00 | +0.00 | 0 | 14748 |
| gemmae4b | gemma | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 8657 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 4 | 1.00 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| insufficient | 6 |

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| gemmae4b | 0.652 | 0.000 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 2.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
