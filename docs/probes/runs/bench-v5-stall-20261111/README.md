# v5 blocked-cycle validation — gemma day3 (2026-11-11)

**Purpose:** confirm the scheduler and probe fixes turn a blocked-only news cycle
into `awaiting_review`/`completed` instead of leaving it `running` forever.

## What ran

- Runtime: Core Pod, rebuilt after the scheduler re-read fix (`1af2448`) and the
  Phase B verifier/template changes.
- Model: `gemma12b` (`gemma4:12b-mlx`).
- Corpus: `eval/bench/v5/v5.yaml`, suite `utility`, `-only v5-news-day3`.
- Probe: `tools/bench` from the same tree.

## Result

```
cycle: v5-news-day3 pass=true state=completed blocks=7/7 (296s)
```

The previous run stalled at `state: running`, `blocks_completed: 4/7`, with
`cycle stalled in state "running": no active tasks`. The new run completed all
seven blocks and passed the external no-change/citation checks.

This validates:

1. `cycleStatusFor` surfaces a blocked-only graph as `awaiting_review` when the
   goal is still `running`.
2. `trackCycle` now re-reads the task graph after `advance`, so it sees the
   blocked leaf rather than the stale pre-advance statuses.
3. The probe treats `awaiting_review` as terminal, not as a running stall.
4. The Phase B news contract (markdown headings + no-repeat-id guard + source
   injection) lets gemma complete the day3 cycle.

## Files

- `cycle-gemma12b.json` — the raw cycle result.
- `cycle-gemma12b.md` — the rendered report.

The full five-task utility rerun is a separate run; this file is a targeted
mechanism check, not a claim-suite result.
