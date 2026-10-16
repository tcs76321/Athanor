# M3-T7 results

Collected 6 job rows.

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | code-bank-atomic | code | single | 1 | 0.00 | 12375 |
| ornith9b | qwen35 | code-expr-eval | code | single | 1 | 0.00 | 7441 |
| ornith9b | qwen35 | code-lru-ttl | code | single | 1 | 0.00 | 11608 |
| ornith9b | qwen35 | code-parse-duration | code | single | 1 | 1.00 | 31374 |
| ornith9b | qwen35 | code-refactor-provided | code | single | 1 | 1.00 | 15285 |
| ornith9b | qwen35 | code-unified-diff | code | single | 1 | 1.00 | 2636 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | single | 4 | 1 | 25% |
| M | single | 2 | 1 | 50% |

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
| ornith9b | single | 0.131 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 0.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
