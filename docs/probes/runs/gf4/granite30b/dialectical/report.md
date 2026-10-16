# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| granite30b | granite | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 6915 |
| granite30b | granite | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 6181 |
| granite30b | granite | local-first-essay | text | 0.00 | 1.00 | +1.00 | 0 | 6790 |
| granite30b | granite | md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 6724 |
| granite30b | granite | onboarding-email | text | 0.00 | 1.00 | +1.00 | 0 | 7000 |
| granite30b | granite | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 7823 |

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
| granite30b | 0.568 | 0.000 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 4.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
