# Run evidence

**Runtime:** Core Pod — **HEADLINE** (supported runtime).

- Model: `bonsai-2:27b`
- Core image: `athanor-bench:latest`
- Core digest: `sha256:1e615e9b71b15d6f1245d438a7d40ac2125e6cbe85e4387af2944c85636e4802`
- Ollama: `0.40.16`
- Revision: `a6a1fbc5acbc52bd6aa0a154b8d4cf53f2ffbdab`
- Started: 2026-11-11T04:05:21Z
- Completed: 2026-11-11T04:08:11Z

| model | digest |
|---|---|
| `bonsai-2:27b` | `f3c4f4208659` |

# M3-T7 results

Collected 2 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (benchmark checks / recorded code test; engine accept is reported separately), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| bonsai27b | bonsai-2 | 2 | 2 | 100% | 0.00 | 8792 | 8792 | 78.6 | 80.4 | 8.0 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| bonsai27b | bonsai-2 | v5-memory-correction | document | single | 1 | 0.00 | 9000 |
| bonsai27b | bonsai-2 | v5-memory-insight | document | single | 1 | 0.00 | 8584 |

## Sequences — checkpoint progress

| goal | arm | progress | steps | final pass |
|---|---|---|---|---|
| v5-memory-correction | single | 100% | 3 | true |
| v5-memory-insight | single | 100% | 2 | true |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| H | single | 2 | 2 | 100% |

**Saturated:** 0/2 completed rows were first-pass successes (no repair, reflection, replan, or observation). They are ranking rows, not evidence for the iterative loop (ADR-0116).

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | single | 2 | 2 | 100% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 2 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| bonsai27b | single | 0.000 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| bonsai27b | 2 | 16 | 15703 | 1881 | 0 | 0 | 0 | 4 | 0 | 0 | 0 | 0 | 0 | 0 | 8 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 4 | 3666 |
| evaluating | 4 | 5434 |
| planning | 4 | 3936 |
| synthesizing | 4 | 4548 |

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
