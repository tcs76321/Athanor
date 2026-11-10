# Benchmark v4 — utility/cycle instrument

v4 measures the 0.x architecture rather than a single generation: a project is
a timeline of cycles, a cycle is a typed block graph, and downstream blocks
consume upstream artifacts. See [ADR-0135](../../../docs/adr/0135-v4-utility-benchmark.md).

This is a **new benchmark version**. v2 is the frozen historical pilot; v3 is
the paired single/cascade/full experiment and stays in the tree. A v4 number is
not comparable to either.

## What a run does

Every task in `brief.yaml` runs under two arms:

- `oneshot` — one engine task on the task's goal. The strong single generation.
- `cycle` — the task's built-in template (`daily-brief`) over its declared
  sources: project → template draft → start → compiled DAG.

The headline is deterministic and external:

- the three required headings are present;
- only the declared source URL appears;
- every substantive paragraph cites it;
- a refusal phrase is absent;
- the word bound holds.

The cycle arm also requires every declared block to complete. A task is labeled
by these checks, never by the engine's own acceptance (ADR-0092).

## Commands

```sh
# pin/verify the model roster and digests
make bench-v4-verify

# one generator, one task
BENCH_V4_ONLY=v4-brief-sqlite make bench-v4-baseline
BENCH_V4_ONLY=v4-brief-sqlite make bench-v4-cycle
make bench-v4-report

# the whole seed suite for one generator
make bench-v4-suite BENCH_V4_MODEL=bonsai27b
```

The Core Pod reads its sources through the gateway. The run config is generated
with `network.allow_list` = `BENCH_V4_ALLOW` (default `sqlite.org,example.com`).
A source outside that list is denied and the task fails, which is the intended
containment behavior.

## Layout

- `protocol.yaml` — arms, suites, roster, pre-registered thresholds.
- `brief.yaml` — the brief corpus (four tasks, one held out).
- `CORPUS.digest` — freeze manifest (`scripts/corpus-digest.sh write eval/bench/v4`).

## Honest limits

The seed is small and directional until the freeze. The `brief` suite is the
first claim suite; build, discovery, and memory suites need their own checks and
fixtures and are added in place. Judges are advisory and do not gate the
headline.
