# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| qwen27b | qwen3.8 | book-collection | code | 1.00 | 0.00 | -1.00 | 4047 | 0 |
| qwen27b | qwen3.8 | fibonacci | code | 1.00 | 0.00 | -1.00 | 3646 | 0 |
| qwen27b | qwen3.8 | local-first-essay | text | 0.00 | 0.00 | +0.00 | 17589 | 0 |
| qwen27b | qwen3.8 | md2html-readme | document | 0.00 | 0.00 | +0.00 | 4900 | 0 |
| qwen27b | qwen3.8 | onboarding-email | text | 0.50 | 0.00 | -0.50 | 9709 | 0 |
| qwen27b | qwen3.8 | sunrise-alarm-brief | document | 0.00 | 0.00 | +0.00 | 5132 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| qwen27b | 0.000 | 0.000 |

## F4 — verification decisions

No completed code accepts in this run.
Comparisons that called the LLM judge: 0.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
