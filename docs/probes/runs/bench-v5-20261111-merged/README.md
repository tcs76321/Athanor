# v5 merged evidence — 2026-11-11

This directory is the **reconciliation** bundle for v5. It merges the original
2×2 run with the post-fix reruns so the committed reports match the evidence
beside them. It is **not** a single-revision freeze run.

## What is merged

| Suite / arm | Source | Note |
|---|---|---|
| foundation single/full | original `tools/bench/results/v5/bonsai27b` | unchanged |
| agency single/full | original | unchanged |
| quality single/full | original | unchanged |
| safety single | post-fix `v5-validation/bonsai27b/safety2` | deterministic verifier authoritative over the LLM security opinion |
| memory single | post-fix `v5-validation/bonsai27b/memory2` | correction injection |
| utility single | original | unchanged |
| utility cycle | post-fix brief + day2 + day3 | **3 of 5 tasks**; day1 and brief-wal are not in the committed evidence |
| Aider single | original | external track, 4/8 bonsai, 1/8 gemma |
| quality-pairwise | original bonsai | granite position bias; quality is `not_measured` |

`catalog.json` is the merge catalog; `bonsai27b/manifest.json` and
`gemma12b/manifest.json` record every copied file and every missing one.

## What the report says now

- Safety is **4/4** for bonsai and gemma (directional; 95% CI 51–100%),
  matching the post-fix reruns. The `v5-safety-path` row is the code task and
  the deterministic test is the label.
- Utility cycle **completion is 3/5 for bonsai** because two reports are
  missing. The component gates correctly fail (`briefs` 1/2, `news_tuning`
  0/2, `news_heldout` 0/1). Do not read the earlier "5/5" claim from this
  bundle; it is not evidenced here.
- **Mechanism coverage is 0%**: four agency rows declare `requires`, and the
  completed runs did not exercise every required mechanism (notably
  `edit_multiple_files` and `repair`). This is a real finding, not a report
  bug.
- **Cost is over the envelope** on the saturated slice (foundation token
  median ~1.65× bonsai, ~2.10× gemma; the gate is 1.5×). This is a real
  finding.
- **Quality is `not_measured`**: the pairwise panel never reached the
  agreement floor (granite picked the first artifact on every order).
- Provenance is **mixed revisions** (foundation/agency at `36be1b9`, safety at
  `4737cf5`, memory at `a6a1fbc`). A freeze requires one run at one commit; this
  bundle does not meet that bar.

## How to reproduce the report and verify it

```sh
make bench-v5-report
make bench-v5-verify
```

`bench-v5-verify` runs `corpus-check-v5`, the analyzer unit tests, the verifier
self-test, and a report-vs-evidence comparison for both models. It is the gate
that fails when a committed report drifts from its rows, when a claim has no
computation path, or when the corpus/protocol digest moved.

## What comes next

1. Fix the news day3 check and the general news template contract (Phase 4).
2. Rerun the full five-task utility suite for both generators.
3. Rerun the affected suites at one commit for the freeze (Phase 5).
4. Replace this bundle (or add a `-frozen` bundle) with a single-revision run
   before setting `cut` to `0.0.0`.
