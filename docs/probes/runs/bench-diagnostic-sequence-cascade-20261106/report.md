# M3-T7 results

Collected 1 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (benchmark checks / recorded code test; engine accept is reported separately), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | 1 | 1 | 100% | 0.00 | 17102 | 17102 | 68.1 | 68.1 | 12.0 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | diag-sequence-memory | document | cascade | 1 | 0.00 | 17102 |

## Sequences — checkpoint progress

| goal | arm | progress | steps | final pass |
|---|---|---|---|---|
| diag-sequence-memory | cascade | 100% | 3 | true |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| X | cascade | 1 | 1 | 100% |

**Saturated:** 0/1 completed rows were first-pass successes (no repair, reflection, replan, or observation). They are ranking rows, not evidence for the iterative loop (ADR-0116).

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | cascade | 1 | 1 | 100% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 1 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | cascade | 0.000 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | 1 | 12 | 15094 | 2008 | 0 | 0 | 0 | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 12 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 3 | 3621 |
| evaluating | 3 | 5006 |
| planning | 3 | 3989 |
| synthesizing | 3 | 4486 |

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
| backup-restore | PASS | snapshot athanor-v0038-20261107T035221Z.db restored and read back (1 project(s)) |

4 passed, 0 failed, 1 skipped.
