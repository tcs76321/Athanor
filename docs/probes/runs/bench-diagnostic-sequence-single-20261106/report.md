# M3-T7 results

Collected 2 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (benchmark checks / recorded code test; engine accept is reported separately), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | 2 | 0 | 0% | 0.00 | 11024 | 0 | 35.9 | 43.1 | 9.0 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | diag-sequence-memory | document | single | 2 | 0.00 | 11024 |

## Sequences — checkpoint progress

| goal | arm | progress | steps | final pass |
|---|---|---|---|---|
| diag-sequence-memory | single | 33% | 2 | false |
| diag-sequence-memory | single | 33% | 2 | false |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| X | single | 2 | 0 | 0% |

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | single | 2 | 0 | 0% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| all-same | 1 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | single | 0.000 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | 2 | 18 | 19684 | 2364 | 2 | 0 | 0 | 4 | 0 | 0 | 0 | 0 | 0 | 0 | 10 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 4 | 3793 |
| evaluating | 6 | 9060 |
| planning | 4 | 4224 |
| refining | 2 | 3000 |
| synthesizing | 2 | 1971 |

## F4 — verification decisions

These are the engine's own accept decisions, reported separately from the
benchmark code label (the recorded frozen-test outcome, ADR-0092).

No completed engine code accepts in this run.
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

## Conformance (ADR-0073 §2)

| scenario | result | detail |
|---|---|---|
| freeze-unfreeze | PASS | freeze → frozen; reasonless unfreeze rejected; reasoned unfreeze → running |
| ui-pages | PASS | dashboard, statistics, projects, corrections render |
| ui-sse | PASS | SSE stream opened (200, text/event-stream) |
| push-local-remote | SKIP | not exercised: no accepted repo-backed artifact with a git commit |
| backup-restore | PASS | snapshot athanor-v0038-20261107T033224Z.db restored and read back (2 project(s)) |

4 passed, 0 failed, 1 skipped.
