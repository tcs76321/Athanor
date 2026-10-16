# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| granite8b | granite | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 8213 |
| granite8b | granite | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 10712 |
| granite8b | granite | local-first-essay | text | 0.00 | 1.00 | +1.00 | 0 | 11342 |
| granite8b | granite | md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 8667 |
| granite8b | granite | onboarding-email | text | 0.00 | 1.00 | +1.00 | 0 | 15058 |
| granite8b | granite | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 11504 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 6 | 1.00 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| insufficient | 6 |

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| granite8b | 0.635 | 0.000 |

## F4 — verification decisions

Code accepts: 1; decided without the LLM judge: 1 (100%).
Comparisons that called the LLM judge: 4.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
