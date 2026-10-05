# M3-T7 — Dialectical-vs-Single-Shot Quality Probe

This is the M3-T7 spike (ROADMAP §7, M3-T7-a/b/c). It
measures the dialectical loop against a single-shot
baseline on three axes:

| Sub-measurement | Question | Tool |
|---|---|---|
| **T-a** Diversity | Are the N divergence candidates actually different? | Average pairwise Jaccard distance over candidate text (`divergence_jaccard` event) |
| **T-b** Calibration | Does the LLM's reported confidence match observed accuracy? | Reliability diagram (confidence-binned observed score) |
| **T-c** Stability at T=0 | Is the verdict reproducible across re-runs? | Winner/score distribution across N runs (unseeded vs seeded) |

The headline experiment (dialectical N=3 vs single-shot N=1) and the
full protocol — locked sample set, models, seed policy, judge channels,
and the memory rule — live in
[`docs/probes/m3-t7-quality-probe.md`](../../docs/probes/m3-t7-quality-probe.md).

## Status: runner implemented

`run` manages the daemon lifecycle and executes the locked matrix;
`report` aggregates the collected rows. The pure analytics (Jaccard
diversity, calibration bins, stability classes), the config/packet
renderers, and the report are unit-tested; the HTTP/DB plumbing is
exercised when the probe actually runs.

`main`'s subcommands:

- `run` — writes each arm's config, starts/stops the daemon with that
  config and a per-arm state dir, submits every (goal × arm × run) job,
  and collects outcomes from the daemon's API and its SQLite database.
- `report` — merges every `results.json` under the output tree and
  renders `report.md`.

## How to run

```sh
# 1. build the daemon binary the runner will start
make build

# 2. smoke test: one goal, one model, both arms
go run -tags sqlite_fts5 ./spikes/m3-t7-probe run -goals 1 -models qwen27b

# 3. the full locked matrix (hours; see the protocol)
go run -tags sqlite_fts5 ./spikes/m3-t7-probe run

# 4. aggregate every results.json into report.md
go run -tags sqlite_fts5 ./spikes/m3-t7-probe report
```

CGO is required: the runner reads the daemon's SQLite database read-only
(`strategy_outcomes` and the proposal artifacts) for captured outcomes.
Per (model, arm) the runner writes `config.yaml`, `daemon.log`,
`results.json`, `results.csv`, `packets/` (blind judge packets plus
`index.json`), and `report.md` under `spikes/m3-t7-probe/results/`.

The judge packets are blind to the arm and model by design; the operator
pastes each `packets/PKT-NNNN.md` into a browser agent and transcribes
the returned score against the packet ID in `packets/index.json`.

## Layout

| File | Role |
|---|---|
| `probe.go` | package doc, HTTP client, subcommand dispatch |
| `matrix.go` | the locked 10-goal set, arms, models, judge selection |
| `probeconfig.go` | renders each arm's daemon config |
| `packets.go` | renders blind judge packets |
| `analysis.go` | pure analytics (Jaccard, calibration bins, stability) |
| `collect.go` | read-only DB + artifact collection |
| `report.go` | pure report aggregation/rendering |
| `runner.go` | daemon lifecycle + matrix execution + output writing |
| `*_test.go` | unit tests for every pure piece |
