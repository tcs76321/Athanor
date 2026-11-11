# Benchmark v5 — grounded, local-first agency

v5 measures the shipped Core Pod on a local-only roster: two generators
(`bonsai-2:27b`, `gemma4:12b-mlx`) and two judges (`granite4.2:8b`,
`mimo-v2.6:9b-mxfp8`). It is a **candidate instrument**, not a release claim;
see [ADR-0136](../../../docs/adr/0136-v5-grounded-agency-benchmark.md).

The headline is deterministic and external: a task is labeled by a probe-owned
re-run of frozen checks/tests, never by the engine's own acceptance or the LLM
judge. Engine acceptance, cycle completion, judge scores, and telemetry are
reported separately (ADR-0092).

## What a run does

- `single` — one engine candidate, no reflection. This is Athanor's engine,
  prompts, tools, and Job Pod; it is not a bare model completion.
- `full` — the dialectical arm: multiple candidates plus comparison/reflection.
- `cycle` — the project's declared block graph (daily brief or news report),
  compiled to a DAG and run by the scheduler.

The corpus is `v5.yaml` (foundation, agency, utility, safety, memory, quality).
Utility tasks use local `sources/` and `wire/` fixtures, not network fixtures.
Cycle tasks are scored on the produced artifact's deterministic checks; cycle
completion is a separate mechanism gate.

## Commands

```sh
# reproducible local run, one generator at a time
scripts/bench-v5.sh bonsai27b
scripts/bench-v5.sh gemma12b

# external validation track (custom Exercism adapter, single arm by default)
BENCH_OLLAMA=http://host.containers.internal:11435 \
  scripts/bench-aider-polyglot.sh bonsai27b single

# 4-exercise full-arm subset
AIDER_HOURS=4 BENCH_OLLAMA=http://host.containers.internal:11435 \
  scripts/bench-aider-polyglot.sh bonsai27b full two-fer,leap,pangram,isogram

# pairwise quality claim
CGO_ENABLED=1 go run -tags sqlite_fts5 ./tools/bench quality-pairwise \
  -single <root>/soak/quality/single/out/artifacts.json \
  -full <root>/soak/quality/full/out/artifacts.json \
  -corpus eval/bench/v5/v5.yaml \
  -out <root>/quality-pairwise.json
```

`scripts/bench-v5.sh` regenerates the report with `eval/bench/v5/analyze.py`.
The analyzer reads the protocol thresholds, omits unrun arms, and reports the
external track separately.

## External validation

`aider-polyglot.yaml` pins a Python subset of the official Exercism exercises
and the source commit. The adapter runs the official tests in a fresh,
network-none container **after Athanor exits**. It is a custom adapter, not the
official Aider harness and not a leaderboard entry. The default is the
8-exercise single arm; a 4-exercise full-arm subset is reported separately.

## Layout

- `protocol.yaml` — roster, arms, suites, claim thresholds.
- `v5.yaml` — the task corpus.
- `fixtures/` — local source/wire and repository fixtures.
- `CORPUS.digest` — freeze manifest (`scripts/corpus-digest.sh write eval/bench/v5`).
- `analyze.py` — claim report; no third-party dependencies.
- `aider-polyglot.yaml` — external track manifest.

## Honest limits

The corpus and external track are small. The quality claim depends on blind
pairwise agreement; pointwise rubric scores are reference only. The external
track is single-arm by default. v5 contains no frontier model and makes no
frontier-parity claim. A failed hard gate is reported red and is never waived.
