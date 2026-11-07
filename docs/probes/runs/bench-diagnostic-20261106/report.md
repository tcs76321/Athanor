# M3-T7 results

Collected 3 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (benchmark checks / recorded code test; engine accept is reported separately), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | 3 | 3 | 100% | 1.00 | 11338 | 11338 | 43.5 | 48.9 | 8.0 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | diag-contradiction-brief | document | full | 1 | 1.00 | 10003 |
| ornith9b | qwen35 | diag-layered-code | code | full | 1 | 1.00 | 12839 |
| ornith9b | qwen35 | diag-structured-brief | document | full | 1 | 1.00 | 11171 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | full | 2 | 2 | 100% |
| X | full | 1 | 1 | 100% |

**Saturated:** 3/3 completed rows were first-pass successes (no repair, reflection, replan, or observation). They are ranking rows, not evidence for the iterative loop (ADR-0116).

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | full | 3 | 3 | 100% |

## Code test label vs engine accept

The code label is the recorded frozen-test outcome (`tests_ran`), not the
engine's accept. Engine accept is reported separately; a test-passing
candidate the engine rejected is a system decision, not a capability
failure. A post-ADR-0092 code row with no recorded test is counted as
test-missing; legacy rows fall back to engine accept, are counted in the
legacy-rows column, and show an engine accept in the last column (ADR-0092).

| model | arm | code rows | test pass | engine accept | test pass, not accepted | test missing | legacy rows | legacy engine accepts |
|---|---|---|---|---|---|---|---|---|
| ornith9b | full | 1 | 1 | 1 | 0 | 0 | 0 | 0 |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 3 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 3 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | full | 0.445 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | 3 | 24 | 28982 | 5031 | 0 | 0 | 0 | 2 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 9 | 10421 |
| evaluating | 9 | 15122 |
| planning | 3 | 3580 |
| synthesizing | 3 | 4890 |

## F4 — verification decisions

These are the engine's own accept decisions, reported separately from the
benchmark code label (the recorded frozen-test outcome, ADR-0092).

Engine code accepts: 1; decided without the LLM judge: 1 (100%).
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
| backup-restore | PASS | snapshot athanor-v0038-20261107T025601Z.db restored and read back (3 project(s)) |

4 passed, 0 failed, 1 skipped.
