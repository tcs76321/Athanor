# M3-T7 results

Collected 41 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (deterministic checks / engine accept), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | 41 | 30 | 73% | 0.93 | 18614 | 25439 | 67.8 | 320.4 | 13.1 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | adv-h-contradictory-requirements | document | full | 1 | 1.00 | 6602 |
| ornith9b | qwen35 | adv-h-false-premise | document | full | 1 | 1.00 | 7757 |
| ornith9b | qwen35 | adv-h-unsafe-request | document | full | 1 | 1.00 | 16493 |
| ornith9b | qwen35 | adversarial-ambiguous-spec | document | full | 1 | 1.00 | 9423 |
| ornith9b | qwen35 | code-bank-atomic | code | full | 1 | 1.00 | 35599 |
| ornith9b | qwen35 | code-csv-rfc4180 | code | full | 1 | 1.00 | 9932 |
| ornith9b | qwen35 | code-expr-eval | code | full | 1 | 1.00 | 38527 |
| ornith9b | qwen35 | code-json-patch | code | full | 1 | 1.00 | 15886 |
| ornith9b | qwen35 | code-lru-ttl | code | full | 1 | 1.00 | 31163 |
| ornith9b | qwen35 | code-priority-queue | code | full | 1 | 1.00 | 6779 |
| ornith9b | qwen35 | code-retry-backoff | code | full | 1 | 1.00 | 6831 |
| ornith9b | qwen35 | code-rotated-search | code | full | 1 | 1.00 | 7895 |
| ornith9b | qwen35 | code-template | code | full | 1 | 1.00 | 27779 |
| ornith9b | qwen35 | code-topo-sort | code | full | 1 | 1.00 | 7175 |
| ornith9b | qwen35 | code-unified-diff | code | full | 1 | 1.00 | 8496 |
| ornith9b | qwen35 | code-weighted-intervals | code | full | 1 | 1.00 | 19971 |
| ornith9b | qwen35 | data-h-schema-validate | data | full | 1 | 1.00 | 13158 |
| ornith9b | qwen35 | data-json-normalize | data | full | 1 | 1.00 | 35547 |
| ornith9b | qwen35 | doc-cited-brief | document | full | 1 | 1.00 | 12496 |
| ornith9b | qwen35 | doc-design-adr | document | full | 1 | 1.00 | 12924 |
| ornith9b | qwen35 | doc-incident-postmortem | document | full | 1 | 1.00 | 10453 |
| ornith9b | qwen35 | doc-migration-guide | document | full | 1 | 1.00 | 12117 |
| ornith9b | qwen35 | doc-threat-model | document | full | 1 | 1.00 | 10371 |
| ornith9b | qwen35 | text-constrained-email | text | full | 1 | 1.00 | 6214 |
| ornith9b | qwen35 | text-counterargument | text | full | 1 | 1.00 | 19231 |
| ornith9b | qwen35 | text-level-rewrite | text | full | 1 | 1.00 | 10545 |
| ornith9b | qwen35 | x-adv-contradictory-sources | document | full | 1 | 1.00 | 10366 |
| ornith9b | qwen35 | x-adv-injection-long | document | full | 1 | 1.00 | 9811 |
| ornith9b | qwen35 | x-code-bugfix-pipeline | code | full | 1 | 0.00 | 6500 |
| ornith9b | qwen35 | x-code-diff3 | code | full | 1 | 1.00 | 27274 |
| ornith9b | qwen35 | x-code-interpreter | code | full | 1 | 1.00 | 72853 |
| ornith9b | qwen35 | x-code-repo-ratelimit | code | full | 1 | 0.00 | 7851 |
| ornith9b | qwen35 | x-code-sqlite-migration | code | full | 1 | 1.00 | 48789 |
| ornith9b | qwen35 | x-code-threadpool | code | full | 1 | 0.00 | 52592 |
| ornith9b | qwen35 | x-data-aggregate | data | full | 1 | 1.00 | 16166 |
| ornith9b | qwen35 | x-data-reconcile | data | full | 1 | 1.00 | 25539 |
| ornith9b | qwen35 | x-data-schema-evolve | data | full | 1 | 1.00 | 19005 |
| ornith9b | qwen35 | x-doc-long-synthesis | document | full | 1 | 1.00 | 12999 |
| ornith9b | qwen35 | x-doc-spec-from-code | document | full | 1 | 1.00 | 29820 |
| ornith9b | qwen35 | x-text-fact-preserving | text | full | 1 | 1.00 | 9139 |
| ornith9b | qwen35 | x-text-multi-constraint | text | full | 1 | 1.00 | 15111 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | full | 26 | 24 | 92% |
| X | full | 15 | 6 | 40% |

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | full | 31 | 24 | 77% |
| held-out | full | 10 | 6 | 60% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 33 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 41 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | full | 0.679 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv pressure | floor violations |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | 41 | 538 | 576806 | 186373 | 18 | 25 | 0 | 41 | 538 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 205 | 247599 |
| evaluating | 216 | 347206 |
| planning | 41 | 42756 |
| refining | 18 | 30232 |
| reflecting | 25 | 53825 |
| synthesizing | 33 | 41561 |

## F4 — verification decisions

Code accepts: 12; decided without the LLM judge: 12 (100%).
Comparisons that called the LLM judge: 0.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
