# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| nemotron30b | nemotron | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 6168 |
| nemotron30b | nemotron | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 6556 |
| nemotron30b | nemotron | local-first-essay | text | 0.00 | 0.66 | +0.66 | 0 | 13022 |
| nemotron30b | nemotron | md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 7886 |
| nemotron30b | nemotron | onboarding-email | text | 0.00 | 1.00 | +1.00 | 0 | 6842 |
| nemotron30b | nemotron | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 8662 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 5 | 1.00 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| insufficient | 6 |

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| nemotron30b | 0.739 | 0.000 |

## F4 — verification decisions

Code accepts: 1; decided without the LLM judge: 1 (100%).
Comparisons that called the LLM judge: 3.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
