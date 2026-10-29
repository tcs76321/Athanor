# M3-T7 results

Collected 6 job rows.

## Model leaderboard (ADR-0073)

Grounded pass rate (deterministic checks / engine accept), cost, and wall-time percentiles per model.

| model | family | jobs | pass | rate | mean score | tok/job | tok/pass | p50 wall (s) | p95 wall (s) | calls/job |
|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | 6 | 3 | 50% | 0.67 | 41266 | 82533 | 108.4 | 506.2 | 17.0 |

## Scores — per (model, goal, arm)

| model | family | goal | archetype | arm | n | mean score | mean tok |
|---|---|---|---|---|---|---|---|
| ornith9b | qwen35 | x-code-bugfix-pipeline | code | full | 1 | 1.00 | 16986 |
| ornith9b | qwen35 | x-code-diff3 | code | full | 1 | 1.00 | 27198 |
| ornith9b | qwen35 | x-code-interpreter | code | full | 1 | 1.00 | 76207 |
| ornith9b | qwen35 | x-code-repo-ratelimit | code | full | 1 | 1.00 | 20402 |
| ornith9b | qwen35 | x-code-sqlite-migration | code | full | 1 | 0.00 | 53211 |
| ornith9b | qwen35 | x-code-threadpool | code | full | 1 | 0.00 | 53595 |

## Corpus — deterministic pass rate (tier × arm)

| tier | arm | n | pass | rate |
|---|---|---|---|---|
| X | full | 6 | 3 | 50% |

## Corpus — held-out vs tuning

The held-out slice is never tuned on. A gain that appears only in the tuning
slice is not evidence of generalization.

| slice | arm | n | pass | rate |
|---|---|---|---|---|
| tuning | full | 4 | 2 | 50% |
| held-out | full | 2 | 1 | 50% |

## T-b — judge-confidence calibration (all arms)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 3 | 1.00 |

## T-c — verdict stability (per arm)

| class | goals |
|---|---|
| insufficient | 6 |

## T-a — candidate diversity (per model × arm)

| model | arm | mean diversity |
|---|---|---|
| ornith9b | full | 0.544 |

## Telemetry — per model (ADR-0073)

| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ornith9b | 6 | 102 | 187726 | 59873 | 4 | 7 | 0 | 6 | 0 | 0 | 0 | 88 | 45 | 12435 | 0 | 0 | 0 |

## Telemetry — calls by phase (all models)

| phase | calls | tokens |
|---|---|---|
| diverging | 39 | 89507 |
| evaluating | 43 | 107113 |
| planning | 6 | 10114 |
| refining | 4 | 11601 |
| reflecting | 7 | 23221 |
| synthesizing | 3 | 6043 |

## F4 — verification decisions

Code accepts: 3; decided without the LLM judge: 3 (100%).
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
