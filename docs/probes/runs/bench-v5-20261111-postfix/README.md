# v5 post-fix validation — 2026-11-11

**Branch:** `bench/v5-0x` · **Runtime:** Core Pod ·
**Generator:** `bonsai-2:27b` · **Judge:** `granite4.2:8b` ·
**Policy:** one scheduler job per task (`max_task_retries: 0`), no hidden retries.

This directory is the first evidence after the P0 engine fixes. It is
validation evidence, not a frozen v5 claim report.

## Results

- **Aider/Exercism single arm: 4/8** external passes for bonsai, **1/8** for
  gemma. In both cases the engine accepted exactly the same rows. This is the
  important result: the engine's test path and the external scorer now agree
  row-for-row. The earlier bonsai 8/8 came from a different shape with
  scheduler retries; these runs are one-shot.
- **Cycle brief (`v5-brief-local-first`): 1/1 external pass, 7/7 blocks,
  cycle completed.** The previous cycle stalled at 5–6/7. A second confirming
  run after the data-capability verifier fix also completed 7/7; see
  `cycle-bonsai27b-brief-local-first-datafix.{md,json}`.
- **Full utility suite:** all **5/5 cycles completed at 7/7 blocks**. External
  pass is **3/5**: both briefs and news day1 pass; day2 and day3 fail their
  news semantics checks. See `utility-cycle-reports/`. The day1 report was
  emitted by the interrupted full-suite run and is recorded in the run tree;
  day2/day3 reports are copied here.
- **Safety:** after the hard-verifier authority fix, **4/4 tasks completed
  and were engine accepted**, including `v5-safety-path`. See
  `safety-bonsai27b-4of4.{md,json}`.
- **Quality:** pairwise judging was run on the existing quality artifacts:
  full win rate 0.75, but inter-judge agreement **0.00** and forward/reverse
  self-consistency 0.30. The quality claim is **not established**. See
  `quality-pairwise-bonsai27b.json`.
- **Memory:** both sequences pass. The correction sequence records
  `correction_injected: true` on the second task. See
  `memory-bonsai27b-2of2.{md,json}`.

## Files

| File | What it is |
|---|---|
| `aider-bonsai27b-single-4of8.{md,json}` | Custom Exercism adapter, single arm, one-shot, engine/external convergence |
| `aider-gemma12b-single-1of8.{md,json}` | Gemma external single arm, one-shot, engine/external convergence |
| `cycle-bonsai27b-brief-local-first.{md,json}` | One brief cycle, completed 7/7 |
| `cycle-bonsai27b-brief-local-first-datafix.{md,json}` | Confirming brief after the data-verifier fix |
| `utility-cycle-reports/` | Day2/day3 and brief cycle reports |
| `safety-bonsai27b-4of4.{md,json}` | Safety suite, 4/4 completed/accepted |
| `memory-bonsai27b-2of2.{md,json}` | Memory sequences, correction injected |
| `quality-pairwise-bonsai27b.json` | Pairwise quality votes and agreement |
| `bonsai27b-v5-report.{md,json}` | Analyzer output with the post-fix external/quality sections |
| `gemma12b-v5-report.{md,json}` | Gemma analyzer output with its external section |

## Freeze decision

**Not ready to freeze.** The P0 engine fixes are validated: external/engine
convergence, safety, cycle completion, and memory all pass. The remaining
reds are measurement/product gaps:

- utility external pass is 3/5, below the 90% pre-registered floor;
- quality pairwise agreement is 0.00, far below the 0.60 floor;
- gemma's one-shot external pass is 1/8 (convergence is exact, model quality is
  low);
- the 4-exercise full-arm external subset and a fresh agency suite have not
  been run.

v5 stays `status: candidate` and `cut: pending`. Do not waive these gates.

## Caveats

- The external track is a custom adapter, not the official Aider harness and
  not a leaderboard entry.
- The external results are one-shot; do not compare them to the earlier
  retry-bearing 8/8 as if they were the same measurement shape.
- The utility external failures are semantic checks, not cycle stalls.
- The full-arm external subset and fresh agency suite are still pending.
