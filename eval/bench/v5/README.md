# Benchmark v5 — grounded, local-first agency

v5 measures the shipped Core Pod on a local-only roster: two generators
(`bonsai-2:27b`, `gemma4:12b-mlx`) and two judges (`granite4.2:8b`,
`mimo-v2.6:9b-mxfp8`). It is the **0.0.0 instrument** ([ADR-0136](../../../docs/adr/0136-v5-grounded-agency-benchmark.md));
it stays `status: candidate` until the freeze run at one revision. If v5 cannot
meet its gates, v6 becomes the 0.0.0 instrument.

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

Each task gets one scheduler job (`max_task_retries: 0`), so a benchmark row is
the outcome of one engine run, not a hidden retry. The `full` arm's multiple
candidates and reflection happen inside that job.

The corpus is `v5.yaml` (foundation, agency, utility, safety, memory, quality).
Utility tasks use local `sources/` and `wire/` fixtures, not network fixtures.
Cycle tasks are scored on the produced artifact's deterministic checks; cycle
completion is a separate mechanism gate.

## Release-blocking claims

- safety: 0 failures (instruction-following; containment is Gate G2/§31.3)
- utility: briefs 2/2, tuning news 2/2, held-out day2 1/1, completion 5/5
- memory: correction/insight injection proven
- mechanism coverage: 1.0 (task requires + full-arm divergence/comparison)
- foundation non-inferiority: full ≥ single − 10 points
- quality: established by two anchor-qualified judges, or `not_measured` and
  removed from the release vector

Cost, agency (saturated/directional), reliability pass@2, and the external
Aider track are reported; they are not release-blocking.

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

# regenerate and verify the committed report
make bench-v5-report
make bench-v5-verify
```

`scripts/bench-v5.sh` regenerates the report with `eval/bench/v5/analyze.py`
after rendering the protocol as JSON (`tools/bench protocol-json`). The analyzer
reports Wilson intervals, directional/saturated flags, the component gates, and
provenance. `verify.py` recomputes the report from the committed rows and fails
on a stale number, a moved corpus/protocol digest, a ghost claim, or missing
provenance.

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
- `verify.py` / `verify_test.py` — report-vs-evidence gate and its unit tests.
- `merge.py` — copies canonical + post-fix runs into a committed evidence tree.
- `aider-polyglot.yaml` — external track manifest.
- `../../../eval/bench/CURRENT` — the instrument 0.0.0 is gated on.

## Honest limits

The corpus and external track are small. The utility claim is component-gated,
not a 90% statistical claim; at n=5 even 5/5 has a Wilson lower bound near 57%.
Quality is `not_measured` until two judges pass the anchor floor and agree; the
pointwise rubric is reference only. The external track is single-arm by default.
v5 contains no frontier model and makes no frontier-parity claim. A failed hard
gate is reported red and is never waived.
