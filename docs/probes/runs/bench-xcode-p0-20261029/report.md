# M3-T7 results

Collected 6 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (deterministic checks / engine accept), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | 6 | 4 | 67% | 0.83 | 30071 | 45106 | 155.7 | 1126.2 | 14.5 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | x-code-bugfix-pipeline | code | full | 1 | 1.00 | 15909 |
| ornith9b | qwen35 | x-code-diff3 | code | full | 1 | 1.00 | 27634 |
| ornith9b | qwen35 | x-code-interpreter | code | full | 1 | 1.00 | 20698 |
| ornith9b | qwen35 | x-code-repo-ratelimit | code | full | 1 | 1.00 | 18438 |
| ornith9b | qwen35 | x-code-sqlite-migration | code | full | 1 | 1.00 | 56864 |
| ornith9b | qwen35 | x-code-threadpool | code | full | 1 | 0.00 | 40881 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| X | full | 6 | 4 | 67% |

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | full | 4 | 2 | 50% |
| held-out | full | 2 | 2 | 100% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 4 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 6 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | full | 0.650 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | 6 | 87 | 132757 | 47667 | 3 | 5 | 0 | 5 | 0 | 0 | 0 | 88 | 45 | 12435 | 0 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 33 | 60882 |
| evaluating | 36 | 78257 |
| planning | 6 | 8370 |
| refining | 3 | 8676 |
| reflecting | 5 | 13139 |
| synthesizing | 4 | 11100 |

## F4 — verification decisions

Code accepts: 4; decided without the LLM judge: 4 (100%).
Comparisons that called the LLM judge: 0.

## Decision diagnostics — external check passed but not accepted

These rows passed the benchmark's own deterministic check yet the engine did not
complete with a new winner. The guard columns show whether the §19.3 guard
downgraded a supported `new`, or the judge itself declined.

| goal | model | state | winner | pre | post | thr | prev | final pass | final conf | reason |
|---|---|---|---|---|---|---|---|---|---|---|
| — | — | — | — | — | — | — | — | — | — | none |

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.

## Conformance (ADR-0073 §2)

| scenario | result | detail |
|---|---|---|
| freeze-unfreeze | PASS | freeze → frozen; reasonless unfreeze rejected; reasoned unfreeze → running |
| ui-pages | PASS | dashboard, statistics, projects, corrections render |
| ui-sse | PASS | SSE stream opened (200, text/event-stream) |
| push-local-remote | PASS | approved push delivered athanor/8d0440ea-3608-46dc-8b74-9ef68be722f7 to a local bare remote |
| backup-restore | PASS | snapshot athanor-v0028-20261029T044555Z.db restored and read back (6 project(s)) |

5 passed, 0 failed, 0 skipped.
