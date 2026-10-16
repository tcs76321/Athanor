# M3-T7 results

Collected 12 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | book-collection | code | 1.00 | 1.00 | +0.00 | 2923 | 10668 |
| ornith9b | qwen35 | fibonacci | code | 1.00 | 1.00 | +0.00 | 6682 | 6755 |
| ornith9b | qwen35 | local-first-essay | text | 1.00 | 1.00 | +0.00 | 4370 | 8187 |
| ornith9b | qwen35 | md2html-readme | document | 1.00 | 1.00 | +0.00 | 4525 | 6929 |
| ornith9b | qwen35 | onboarding-email | text | 1.00 | 1.00 | +0.00 | 4105 | 7827 |
| ornith9b | qwen35 | sunrise-alarm-brief | document | 1.00 | 1.00 | +0.00 | 7775 | 9348 |

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
| ornith9b | 0.750 | 0.000 |

## F4 — verification decisions

Code accepts: 4; decided without the LLM judge: 4 (100%).
Comparisons that called the LLM judge: 8.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
