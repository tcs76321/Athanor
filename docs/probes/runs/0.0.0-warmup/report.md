# M3-T7 results

Collected 30 job rows.

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | adversarial-ambiguous-spec | document | full | 2 | 1.00 | 7838 |
| ornith9b | qwen35 | code-bank-atomic | code | full | 2 | 1.00 | 21606 |
| ornith9b | qwen35 | code-expr-eval | code | full | 2 | 1.00 | 12033 |
| ornith9b | qwen35 | code-lru-ttl | code | full | 2 | 1.00 | 10406 |
| ornith9b | qwen35 | code-parse-duration | code | full | 2 | 0.00 | 18886 |
| ornith9b | qwen35 | code-refactor-provided | code | full | 2 | 1.00 | 15339 |
| ornith9b | qwen35 | code-unified-diff | code | full | 2 | 1.00 | 15322 |
| ornith9b | qwen35 | data-json-normalize | data | full | 2 | 1.00 | 9302 |
| ornith9b | qwen35 | doc-api-readme | document | full | 2 | 0.00 | 21298 |
| ornith9b | qwen35 | doc-changelog-categorize | document | full | 2 | 1.00 | 29028 |
| ornith9b | qwen35 | doc-cited-brief | document | full | 2 | 0.50 | 16239 |
| ornith9b | qwen35 | doc-design-adr | document | full | 2 | 1.00 | 14945 |
| ornith9b | qwen35 | text-argued-essay | text | full | 2 | 1.00 | 14422 |
| ornith9b | qwen35 | text-constrained-email | text | full | 2 | 1.00 | 10176 |
| ornith9b | qwen35 | text-level-rewrite | text | full | 2 | 0.00 | 19712 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | full | 20 | 5 | 25% |
| M | full | 10 | 1 | 10% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 18 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| all-same | 11 |
| all-distinct | 4 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | full | 0.790 |

## F4 — verification decisions

Code accepts: 1; decided without the LLM judge: 1 (100%).
Comparisons that called the LLM judge: 13.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
