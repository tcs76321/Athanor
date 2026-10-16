# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| granite3b | granite | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 6889 |
| granite3b | granite | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 7466 |
| granite3b | granite | local-first-essay | text | 0.00 | 0.50 | +0.50 | 0 | 13838 |
| granite3b | granite | md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 11436 |
| granite3b | granite | onboarding-email | text | 0.00 | 1.00 | +1.00 | 0 | 8099 |
| granite3b | granite | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 7387 |

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
| granite3b | 0.653 | 0.000 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 3.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
