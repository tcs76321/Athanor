# M3-T7 results

Collected 30 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | always-reverse | code | 0.00 | 0.50 | +0.50 | 0 | 11804 |
| ornith9b | qwen35 | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 7170 |
| ornith9b | qwen35 | cache-class | code | 0.00 | 0.67 | +0.67 | 0 | 15331 |
| ornith9b | qwen35 | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 7550 |
| ornith9b | qwen35 | local-first-essay | text | 0.00 | 1.00 | +1.00 | 0 | 8656 |
| ornith9b | qwen35 | md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 7321 |
| ornith9b | qwen35 | onboarding-email | text | 0.00 | 1.00 | +1.00 | 0 | 7634 |
| ornith9b | qwen35 | stringutils | code | 0.00 | 1.00 | +1.00 | 0 | 6101 |
| ornith9b | qwen35 | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 9269 |
| ornith9b | qwen35 | todo-list | code | 0.00 | 1.00 | +1.00 | 0 | 7646 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 26 | 1.00 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| 3-same | 4 |
| 2-1 | 6 |

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| ornith9b | 0.681 | 0.000 |

## F4 — verification decisions

Code accepts: 12; decided without the LLM judge: 12 (100%).
Comparisons that called the LLM judge: 12.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
