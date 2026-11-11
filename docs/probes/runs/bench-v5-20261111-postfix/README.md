# v5 post-fix validation — 2026-11-11

**Branch:** `bench/v5-0x` · **Runtime:** Core Pod ·
**Generator:** `bonsai-2:27b` · **Judge:** `granite4.2:8b` ·
**Policy:** one scheduler job per task (`max_task_retries: 0`), no hidden retries.

This directory is the first evidence after the P0 engine fixes. It is
validation evidence, not a frozen v5 claim report.

## Results

- **Aider/Exercism single arm: 4/8** external passes, and **engine accepted
  exactly the same 4/8**. This is the important result: the engine's test path
  and the external scorer now agree row-for-row. The earlier 8/8 came from a
  different shape with scheduler retries; these 8 exercises are one-shot.
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

## Files

| File | What it is |
|---|---|
| `aider-bonsai27b-single-4of8.{md,json}` | Custom Exercism adapter, single arm, one-shot, engine/external convergence |
| `cycle-bonsai27b-brief-local-first.{md,json}` | One brief cycle, completed 7/7 |
| `bonsai27b-v5-report.{md,json}` | Analyzer output with the post-fix external section |

## Caveats

- The external track is a custom adapter, not the official Aider harness and
  not a leaderboard entry.
- The 4/8 result is one-shot; the model failed different exercises than in the
  retry-bearing exploratory run. Do not compare the two as if they were the
  same measurement shape.
- The utility suite is only validated on one brief here; the full suite result
  lives in the run tree until copied.
