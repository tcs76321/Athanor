# M3-T7 results

Collected 6 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (deterministic checks / engine accept), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| bonsai9b | qwen35 | 6 | 2 | 33% | 0.33 | 42386 | 127158 | 132.3 | 888.6 | 17.0 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| bonsai9b | qwen35 | x-code-bugfix-pipeline | code | full | 1 | 1.00 | 14130 |
| bonsai9b | qwen35 | x-code-diff3 | code | full | 1 | 1.00 | 18341 |
| bonsai9b | qwen35 | x-code-interpreter | code | full | 1 | 0.00 | 110784 |
| bonsai9b | qwen35 | x-code-repo-ratelimit | code | full | 1 | 0.00 | 51522 |
| bonsai9b | qwen35 | x-code-sqlite-migration | code | full | 1 | 0.00 | 0 |
| bonsai9b | qwen35 | x-code-threadpool | code | full | 1 | 0.00 | 59539 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| X | full | 6 | 2 | 33% |

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | full | 4 | 1 | 25% |
| held-out | full | 2 | 1 | 50% |

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
| bonsai9b | full | 0.595 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| bonsai9b | 5 | 85 | 179461 | 74855 | 3 | 6 | 0 | 2 | 0 | 0 | 0 | 0 | 73 | 24955 | 0 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 33 | 99366 |
| evaluating | 36 | 111013 |
| planning | 5 | 11883 |
| refining | 3 | 13135 |
| reflecting | 6 | 15312 |
| synthesizing | 2 | 3607 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
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
| push-local-remote | PASS | approved push delivered athanor/562c3af8-b1a8-4635-b2e4-9439fa1f17bf to a local bare remote |
| backup-restore | PASS | snapshot athanor-v0030-20261029T184813Z.db restored and read back (6 project(s)) |

5 passed, 0 failed, 0 skipped.
