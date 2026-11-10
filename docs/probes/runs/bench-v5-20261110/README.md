# v5 run evidence — 2026-11-10

**Branch:** `bench/v5-0x` · **Commit at run:** `926f336` ·
**Runtime:** Core Pod · **Generators:** `bonsai-2:27b`, `gemma4:12b-mlx` ·
**Judges:** `granite4.2:8b`, `mimo-v2.6:9b-mxfp8` ·
**Issue register:** [docs/v5-known-issues.md](../../../v5-known-issues.md)

This directory preserves the small reports from the first v5 2×2 and the
Aider/Exercism external track. Large artifacts, job logs, and per-trial
trajectories remain in the gitignored run trees
(`tools/bench/results/v5/`, `.bench-aider/`).

## Files

| File | What it is |
|---|---|
| `bonsai27b-v5-report.{md,json}` | v5 analyzer output for bonsai27b |
| `gemma12b-v5-report.{md,json}` | v5 analyzer output for gemma12b |
| `*-quality-single-scores.json` | two-judge rubric scores, single arm |
| `*-quality-full-scores.json` | two-judge rubric scores, full arm |
| `aider-bonsai27b-single-4of4.{md,json}` | Aider/Exercism Python subset, exercises 1–4, single arm, **4/4** |
| `aider-bonsai27b-single-3of4.{md,json}` | Aider/Exercism Python subset, exercises 5–8, single arm, **3/4**; acronym missed on an index timeout (retry pending) |

## Headline results

- **Foundation:** 83% single → 92% full for both generators; +8.3 points,
  1 win / 11 ties / 0 losses. Full ~1.8–2.1× tokens.
- **Agency:** 100% both arms, both generators — **saturated**.
- **Utility:** external cycle pass 60% bonsai / 40% gemma vs single 40% / 20%;
  **cycle completion 0% / 40%**. Not established.
- **Safety:** gemma 4/4; bonsai 3/4 (`v5-safety-path`). The 2×2 safety gate fails.
- **Memory:** 50% both.
- **Quality:** deterministic 83% full both; judge agreement
  bonsai −0.667/−0.490, gemma 0.544/−0.500. **Not established.**
- **External:** 7/8 Exercism Python exercises, 71 official assertions, single
  arm, custom adapter. Acronym retry pending.

## Caveats

- The Aider track is a **custom adapter**, not the official Aider harness, and
  not a leaderboard entry.
- The Aider single arm generated correct code that the engine's own
  acceptance/test path did not accept; this is the P0 issue in the register.
- The external track is single-arm by default; the full arm exceeded the 2h cap
  at 6/8 exercises.
- Reports are the analyzer's output at the time; rerun
  `eval/bench/v5/analyze.py` after adding a merged Aider report.
