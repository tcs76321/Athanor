# Probe run evidence (committed summaries)

This directory holds committed copies of the M3-T7 probe's rolling
`report.md` files — one per run directory — so `v0.0.0` ships with the run
evidence attached. They are **copies** of generated output; the live tree they
come from is `tools/bench/results/`, which is **gitignored** (generated
configs, daemon logs, packets, the 59 MB of `results.csv`/`soak.csv`/
`metrics.jsonl`). A fresh clone gets these summaries, not the raw data.

Regenerate the copies after a run:

```sh
rsync -a --include='*/' --include='report.md' --exclude='*' \
  tools/bench/results/ docs/probes/runs/
```

The curated write-ups in `docs/probes/` are the durable record; these files are
the raw generator output they quote. Where a claim matters, cite the curated
doc (which carries the interpretation and caveats) and point here for the
numbers.

## Evidence ledger

| Claim | Curated doc | Run here | Raw (gitignored) |
|---|---|---|---|
| M1 single-shot: 5/5 criteria adherence (gemma12b) | `docs/probes/m1-quality-probe.md` | *(M1 probe predates the results tree)* | `state-probe/` |
| M3-T7: loop ties 7/10, moves ≤ ±0.08, ~1.8× tokens; verifier is the same model | `docs/probes/m3-t7-quality-probe-results.md` | `main2/report.md` | `results/main2/` |
| Judge reliability/saturation; expanded anchor 0.61 (gemma), 0.53 (granite), generator 0.77 | `docs/probes/f4-anchor-calibration.md`, `f4-verifier-run.md` | `gf4/report.md` (T-b) | `results/anchor*/`, `results/gf4/` |
| F4 verifier-mode: 7/7 code accepts decided without the LLM judge | `docs/probes/f4-verifier-run.md` | `f4verifier/report.md`, `f4multigen/report.md` | `results/f4verifier/`, `results/f4multigen/` |
| G-F4 closeout (10 models): 42/42 code accepts deterministic; no consistent dialectical advantage on bounded tasks | `docs/probes/gf4-closeout.md` | `gf4/report.md` | `results/gf4/`, `results/gf4-drift/` |
| M9 soak: same model 6/6 → 0/2; guidance channel inert; kill -9 recovers in ~8 s | `docs/soak-m9-findings.md` | `soak-m9/report.md` | `results/soak-m9/` |
| Model qualification + routing recommendation (granite4.2:3b best judge; gemma12b/granite3b) | `docs/qualification-smoke-m9.md` | `gen-smoke-m9/report.md`, `gen-smoke-m9-2/` | `results/gen-smoke-m9*/` |
| Engine throughput: M3 = 1.94× M1 per-job, 9 LLM calls/job | `docs/benchmarks/engine-m1.txt`, `engine-m3.txt` | *(not probe output; `make bench`)* | — |
| Declared checks mirror the corpus scorer (ADR-0070) | `docs/adr/0070-declared-checks.md` | `eval/bench/tasks.yaml` | — |

## Turning a run into a routing proposal

```sh
make probe-measure            # writes results/measurements.json (onboard.Measurement[])
athanor models -qualification tools/bench/results/measurements.json
```

`measure` records `Samples` per score, so `onboard.Route`'s `MinSamples` guard
can refuse a single-pass pass@k — the M9 soak's 6/6 → 0/2 swing is the reason
that guard exists.
