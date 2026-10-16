# M3-T7 results

Collected 6 job rows.

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | code-bank-atomic | code | dialectical | 1 | 0.00 | 28586 |
| ornith9b | qwen35 | code-expr-eval | code | dialectical | 1 | 0.00 | 14878 |
| ornith9b | qwen35 | code-lru-ttl | code | dialectical | 1 | 0.00 | 12012 |
| ornith9b | qwen35 | code-parse-duration | code | dialectical | 1 | 1.00 | 29607 |
| ornith9b | qwen35 | code-refactor-provided | code | dialectical | 1 | 1.00 | 30126 |
| ornith9b | qwen35 | code-unified-diff | code | dialectical | 1 | 1.00 | 22666 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | dialectical | 4 | 1 | 25% |
| M | dialectical | 2 | 1 | 50% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 2 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 6 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | dialectical | 0.569 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 0.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
