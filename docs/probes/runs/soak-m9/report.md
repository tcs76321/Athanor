# M3-T7 results

Collected 18 job rows.

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| gemma12b | gemma | adversarial-ambiguous-spec | document | full | 1 | 1.00 | 6926 |
| gemma12b | gemma | code-bank-atomic | code | full | 2 | 0.50 | 21419 |
| gemma12b | gemma | code-expr-eval | code | full | 2 | 1.00 | 25122 |
| gemma12b | gemma | code-lru-ttl | code | full | 2 | 0.50 | 5022 |
| gemma12b | gemma | code-parse-duration | code | full | 1 | 1.00 | 12069 |
| gemma12b | gemma | code-refactor-provided | code | full | 1 | 1.00 | 10512 |
| gemma12b | gemma | code-unified-diff | code | full | 1 | 1.00 | 22267 |
| gemma12b | gemma | data-json-normalize | data | full | 1 | 1.00 | 17413 |
| gemma12b | gemma | doc-api-readme | document | full | 1 | 0.00 | 13869 |
| gemma12b | gemma | doc-changelog-categorize | document | full | 1 | 0.00 | 15782 |
| gemma12b | gemma | doc-cited-brief | document | full | 1 | 0.00 | 15899 |
| gemma12b | gemma | doc-design-adr | document | full | 1 | 1.00 | 12337 |
| gemma12b | gemma | text-argued-essay | text | full | 1 | 1.00 | 8694 |
| gemma12b | gemma | text-constrained-email | text | full | 1 | 1.00 | 6783 |
| gemma12b | gemma | text-level-rewrite | text | full | 1 | 0.00 | 14297 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | full | 13 | 6 | 46% |
| M | full | 5 | 3 | 60% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 12 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| all-distinct | 3 |
| insufficient | 12 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| gemma12b | full | 0.613 |

## F4 — verification decisions

Code accepts: 6; decided without the LLM judge: 6 (100%).
Comparisons that called the LLM judge: 5.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
