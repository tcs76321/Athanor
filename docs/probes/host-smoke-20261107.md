# Host smoke — 2026-11-07

**Status:** diagnostic · **Runtime:** host binary (not the Core Pod) ·
**Result tree:** `tools/bench/results/host-smoke/` (gitignored)

## What ran

The six-task engine-surface smoke, not a headline slice (ADR-0126). One
canonical 9B generator (`ornith-1.5:9b`) and a cross-family judge
(`granite4.2:3b`), one pass, `arm=full`.

| Task | Tier | Result |
|---|---|---|
| `code-bank-atomic` | H | pass |
| `code-expr-eval` | H | fail (recorded code test) |
| `x-code-interpreter` | X (held out) | fail (recorded code test) |
| `doc-cited-brief` | H | pass |
| `text-level-rewrite` | H | pass |
| `data-json-normalize` | H | pass |

**4/6 deterministic.** The conformance matrix passed 5/5
(freeze/unfreeze, UI pages, SSE, local push, backup/restore). Wall clock was
about 18 minutes; the run wrote `run.json` with `runtime=host` and
`diagnostic=true`.

## Caveats

- **Host path.** This is a development smoke, not a Core Pod headline. The
  report is labeled diagnostic and `report -headline` refuses it.
- **One pass, one model.** The two code failures are consistent with the known
  X/code difficulty on a 9B model; they are not a regression signal.
- **No co-residency or swarm measurement.** This run used one generator and one
  judge; the capacity plan and multi-slot experiment are separate work.
- The `code-expr-eval` and `x-code-interpreter` failures are recorded by the
  frozen test command, not by the engine's verifier.

## What changed because of the run

- `run.json` records runtime/model/digest/revision evidence.
- The report header now says `DIAGNOSTIC` for host runs.
- The final report is rendered after run metadata is finalized, so a completed
  run no longer says “Incomplete.”

Raw evidence: `tools/bench/results/host-smoke/report.md`, `run.json`,
`results.json`, and `scenarios.json`.
