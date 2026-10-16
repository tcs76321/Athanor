# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| qwen27b | qwen3.8 | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 17568 |
| qwen27b | qwen3.8 | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 7327 |
| qwen27b | qwen3.8 | local-first-essay | text | 0.00 | 1.00 | +1.00 | 0 | 13062 |
| qwen27b | qwen3.8 | md2html-readme | document | 0.00 | 0.00 | +0.00 | 0 | 781 |
| qwen27b | qwen3.8 | onboarding-email | text | 0.00 | 1.00 | +1.00 | 0 | 25790 |
| qwen27b | qwen3.8 | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 32289 |

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
| qwen27b | 0.722 | 0.000 |

## F4 — verification decisions

No completed code accepts in this run.
Comparisons that called the LLM judge: 3.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
