# M3-T7 results

Collected 10 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | always-reverse | code | 0.00 | 0.00 | +0.00 | 4992 | 0 |
| ornith9b | qwen35 | book-collection | code | 1.00 | 0.00 | -1.00 | 3307 | 0 |
| ornith9b | qwen35 | cache-class | code | 0.50 | 0.00 | -0.50 | 5927 | 0 |
| ornith9b | qwen35 | fibonacci | code | 1.00 | 0.00 | -1.00 | 7047 | 0 |
| ornith9b | qwen35 | local-first-essay | text | 1.00 | 0.00 | -1.00 | 6200 | 0 |
| ornith9b | qwen35 | md2html-readme | document | 1.00 | 0.00 | -1.00 | 7372 | 0 |
| ornith9b | qwen35 | onboarding-email | text | 1.00 | 0.00 | -1.00 | 4038 | 0 |
| ornith9b | qwen35 | stringutils | code | 1.00 | 0.00 | -1.00 | 5237 | 0 |
| ornith9b | qwen35 | sunrise-alarm-brief | document | 1.00 | 0.00 | -1.00 | 5196 | 0 |
| ornith9b | qwen35 | todo-list | code | 1.00 | 0.00 | -1.00 | 4080 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| ornith9b | 0.000 | 0.000 |

## F4 — verification decisions

Code accepts: 3; decided without the LLM judge: 3 (100%).
Comparisons that called the LLM judge: 3.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
