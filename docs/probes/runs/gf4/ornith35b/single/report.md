# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| ornith35b | qwen35 | book-collection | code | 1.00 | 0.00 | -1.00 | 3635 | 0 |
| ornith35b | qwen35 | fibonacci | code | 1.00 | 0.00 | -1.00 | 3658 | 0 |
| ornith35b | qwen35 | local-first-essay | text | 1.00 | 0.00 | -1.00 | 4623 | 0 |
| ornith35b | qwen35 | md2html-readme | document | 1.00 | 0.00 | -1.00 | 3555 | 0 |
| ornith35b | qwen35 | onboarding-email | text | 1.00 | 0.00 | -1.00 | 4498 | 0 |
| ornith35b | qwen35 | sunrise-alarm-brief | document | 1.00 | 0.00 | -1.00 | 4688 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| ornith35b | 0.000 | 0.000 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 4.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
