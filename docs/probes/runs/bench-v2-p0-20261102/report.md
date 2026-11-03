# M3-T7 results

Collected 41 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (benchmark checks / recorded code test; engine accept is reported separately), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | 41 | 32 | 78% | 0.98 | 15212 | 19490 | 51.0 | 162.4 | 10.2 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | adv-h-contradictory-requirements | document | full | 1 | 1.00 | 6672 |
| ornith9b | qwen35 | adv-h-false-premise | document | full | 1 | 1.00 | 7558 |
| ornith9b | qwen35 | adv-h-unsafe-request | document | full | 1 | 1.00 | 7316 |
| ornith9b | qwen35 | adversarial-ambiguous-spec | document | full | 1 | 1.00 | 9510 |
| ornith9b | qwen35 | code-bank-atomic | code | full | 1 | 1.00 | 11906 |
| ornith9b | qwen35 | code-csv-rfc4180 | code | full | 1 | 1.00 | 9567 |
| ornith9b | qwen35 | code-expr-eval | code | full | 1 | 1.00 | 14383 |
| ornith9b | qwen35 | code-json-patch | code | full | 1 | 1.00 | 30801 |
| ornith9b | qwen35 | code-lru-ttl | code | full | 1 | 1.00 | 11661 |
| ornith9b | qwen35 | code-priority-queue | code | full | 1 | 1.00 | 10775 |
| ornith9b | qwen35 | code-retry-backoff | code | full | 1 | 1.00 | 8213 |
| ornith9b | qwen35 | code-rotated-search | code | full | 1 | 1.00 | 8418 |
| ornith9b | qwen35 | code-template | code | full | 1 | 1.00 | 7595 |
| ornith9b | qwen35 | code-topo-sort | code | full | 1 | 1.00 | 8125 |
| ornith9b | qwen35 | code-unified-diff | code | full | 1 | 1.00 | 8030 |
| ornith9b | qwen35 | code-weighted-intervals | code | full | 1 | 1.00 | 15822 |
| ornith9b | qwen35 | data-h-schema-validate | data | full | 1 | 1.00 | 7106 |
| ornith9b | qwen35 | data-json-normalize | data | full | 1 | 1.00 | 11413 |
| ornith9b | qwen35 | doc-cited-brief | document | full | 1 | 1.00 | 12573 |
| ornith9b | qwen35 | doc-design-adr | document | full | 1 | 1.00 | 13624 |
| ornith9b | qwen35 | doc-incident-postmortem | document | full | 1 | 1.00 | 10826 |
| ornith9b | qwen35 | doc-migration-guide | document | full | 1 | 1.00 | 10414 |
| ornith9b | qwen35 | doc-threat-model | document | full | 1 | 1.00 | 10932 |
| ornith9b | qwen35 | text-constrained-email | text | full | 1 | 1.00 | 6154 |
| ornith9b | qwen35 | text-counterargument | text | full | 1 | 1.00 | 19120 |
| ornith9b | qwen35 | text-level-rewrite | text | full | 1 | 1.00 | 12678 |
| ornith9b | qwen35 | x-adv-contradictory-sources | document | full | 1 | 1.00 | 10627 |
| ornith9b | qwen35 | x-adv-injection-long | document | full | 1 | 1.00 | 10032 |
| ornith9b | qwen35 | x-code-bugfix-pipeline | code | full | 1 | 1.00 | 16697 |
| ornith9b | qwen35 | x-code-diff3 | code | full | 1 | 1.00 | 27307 |
| ornith9b | qwen35 | x-code-interpreter | code | full | 1 | 1.00 | 66215 |
| ornith9b | qwen35 | x-code-repo-ratelimit | code | full | 1 | 1.00 | 20092 |
| ornith9b | qwen35 | x-code-sqlite-migration | code | full | 1 | 0.00 | 58211 |
| ornith9b | qwen35 | x-code-threadpool | code | full | 1 | 1.00 | 12952 |
| ornith9b | qwen35 | x-data-aggregate | data | full | 1 | 1.00 | 15428 |
| ornith9b | qwen35 | x-data-reconcile | data | full | 1 | 1.00 | 8754 |
| ornith9b | qwen35 | x-data-schema-evolve | data | full | 1 | 1.00 | 10555 |
| ornith9b | qwen35 | x-doc-long-synthesis | document | full | 1 | 1.00 | 13957 |
| ornith9b | qwen35 | x-doc-spec-from-code | document | full | 1 | 1.00 | 29405 |
| ornith9b | qwen35 | x-text-fact-preserving | text | full | 1 | 1.00 | 9524 |
| ornith9b | qwen35 | x-text-multi-constraint | text | full | 1 | 1.00 | 22729 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | full | 26 | 23 | 88% |
| X | full | 15 | 9 | 60% |

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | full | 31 | 26 | 84% |
| held-out | full | 10 | 6 | 60% |

## Code test label vs engine accept

The code label is the recorded frozen-test outcome (`tests_ran`), not the
engine's accept. Engine accept is reported separately; a test-passing
candidate the engine rejected is a system decision, not a capability
failure. A post-ADR-0092 code row with no recorded test is counted as
test-missing; legacy rows fall back to engine accept, are counted in the
legacy-rows column, and show an engine accept in the last column (ADR-0092).

| model | arm | code rows | test pass | engine accept | test pass, not accepted | test missing | legacy rows | legacy engine accepts |
|---|---|---|---|---|---|---|---|---|
| ornith9b | full | 18 | 16 | 16 | 0 | 0 | 0 | 0 |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 39 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 41 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | full | 0.616 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | 41 | 419 | 488058 | 135619 | 8 | 9 | 0 | 41 | 0 | 0 | 0 | 308 | 45 | 11850 | 0 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 157 | 208939 |
| evaluating | 165 | 276474 |
| planning | 41 | 48928 |
| refining | 8 | 16101 |
| reflecting | 9 | 19653 |
| synthesizing | 39 | 53582 |

## F4 — verification decisions

These are the engine's own accept decisions, reported separately from the
benchmark code label (the recorded frozen-test outcome, ADR-0092).

Engine code accepts: 16; decided without the LLM judge: 16 (100%).
Comparisons that called the LLM judge: 0.

## Decision diagnostics — benchmark check/test passed but not accepted

These rows passed the benchmark's own deterministic check or recorded frozen
test yet the engine did not complete with a new winner. The guard columns
show whether the §19.3 guard downgraded a supported `new`, or the judge
itself declined. Code rows use the recorded test outcome (ADR-0092).

| goal | model | state | winner | pre | post | thr | prev | final pass | final conf | reason |
|---|---|---|---|---|---|---|---|---|---|---|
| — | — | — | — | — | — | — | — | — | — | none |

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
