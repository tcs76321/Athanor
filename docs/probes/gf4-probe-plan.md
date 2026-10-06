# G-F4 closeout probe — runbook

> **This is the probe, not the soak.** The 24h endurance soak is M7-T9
> (`make soak`, Gate G7). G-F4 is closed by the *quality probe*: the
> dialectical-vs-single-shot run, the judge/anchor measurements, and the
> T-a/b/c diagnostics.

**Goal:** close Gate G-F4 by producing measured evidence for its arms, and
either flip it in `ROADMAP.md` or record precisely which arm remains open.

**Prerequisites:** Apple-silicon host; Ollama with the models below; Podman
with `python:3.12-alpine` present; `make build` run; AC power; several hours.

## Gate G-F4 arms and how each is measured

| Arm | Measured by | Floor |
|---|---|---|
| Real code tests | P2 (code goals run real per-goal tests) | pass/fail is real |
| Reliable judges | P1 reliability per judge | ≥ 0.80 |
| Non-degenerate judge scores | P1 (low=1 / high=5 anchors present) | not constant; Spearman > 0 |
| Human-anchor agreement | P1 vs `eval/anchor/ratings.csv` | ≥ `min_anchor_agreement` (0.60) |
| Verifiers decide code accepts | P2 deterministic-accept fraction | ≥ 50% |
| Compute scales with difficulty | F4-T2 unit tests | (already green) |
| Diversity enforced | P2 T-a mean Jaccard | > 0 |
| No call can stall | F4-T8 unit tests | (already green) |

## Preconditions (do first)

1. **F5 gates are pinned off in the probe config** (`probeconfig.go` emits
   `require_tests_for_code: false` / `require_documentation_for_code: false`).
   Rationale: the F5 docs gate is a heuristic that would reject valid code
   goals whose criteria omit docstrings (`cache-class`, `always-reverse`),
   confounding the N=1-vs-N=3 headline. Run the gates separately with
   `-gates` (the F5 validation arm) if wanted.
2. **Engine outcome capture is atomic with the terminal state** (`engine.go`
   captures the strategy outcome before pod teardown; the failure path also
   stops its pod). This removes the M3-T7 code-goal reconciliation gap.
3. **Human-rate the anchor.** `eval/anchor/ratings.csv` currently holds 16
   rows: 4 objective (code pass/fail) and 12 `rater=agent` (provisional,
   machine). Add **human** rows (any non-`agent` rater name) by rating each
   `eval/anchor/cases.md` case blind (goal + criteria + artifact only). The
   loader prefers human rows and ignores the agent rows for that case. This is
   what makes "agreement with the human anchor" honest.
4. **Environment:**
   - `OLLAMA_MAX_LOADED_MODELS=1`, `OLLAMA_NUM_PARALLEL=1` (single residency;
     the 27–35 B judges are 18–22 GB).
   - Record model **digests** (`ollama list`) and the Ollama version.
   - `make build`; confirm `podman images | grep python:3.12-alpine`.
   - Free disk: at least a few GB (artifacts + state per arm).

**Judge set available on this host:** `ornith-1.5:9b` (the generator),
`ornith-1.5:35b` (same family, high capability — the clean control for the
family/capability confound), `qwen3.8:27b-mlx`, `gemma4:12b-mlx`,
`granite4.2:30b`, `granite4.2:3b`.

## P1 — Anchor calibration (start here; fast)

```sh
make build
go run -tags sqlite_fts5 ./spikes/m3-t7-probe anchor \
  -protocol pointwise5 \
  -judges ornith-1.5:9b,ornith-1.5:35b,qwen3.8:27b-mlx,gemma4:12b-mlx,granite4.2:30b,granite4.2:3b \
  -think false -num-ctx 8192
```

- Output: per-judge reliability (ok/rate), Spearman, Kendall vs the human
  ratings; exits non-zero if any judge is below the floor.
- Interpretation: if `ornith-1.5:35b` (same family, high capability) and
  `qwen3.8:27b` (cross family, high capability) score similarly and above the
  smaller cross-family judges, the signal is **capability**, not family — the
  defensible claim from `docs/probes/f4-verifier-run.md` §C1.
- Re-run the old 8-case comparison only if needed; the 16-case set is the
  corrected anchor.

## P2 — Headline: dialectical vs single-shot (the main event)

```sh
go run -tags sqlite_fts5 ./spikes/m3-t7-probe run \
  -models ornith9b -judge-mode verifier -max-wall 10h
```

- Runs 10 goals × (dialectical ×3 + single ×1) = 40 jobs with real code tests.
- Optional companion: `-no-reflect` for a pure single-shot baseline arm.
- Optional F5 validation arm: re-run a subset with `-gates -only cache-class,always-reverse,book-collection`
  to measure whether the new docs/test gates reject candidates a human would
  accept (the F5 false-negative question). Keep it **separate** from the
  headline.

## P3 — Score and aggregate

```sh
go run -tags sqlite_fts5 ./spikes/m3-t7-probe judge
go run -tags sqlite_fts5 ./spikes/m3-t7-probe report
go run -tags sqlite_fts5 ./spikes/m3-t7-probe bundle   # labeled artifact bundle
```

- `report.md` fills the headline Δ table, T-a diversity, T-b calibration bin
  table, T-c stability tally, and the F4 verification-decision summary.
- Read `artifacts.md` to sanity-check any surprising winner.

## P4 — Optional: correlated-error / candidate-ranking experiment

The gate text does not require it. ADR-0045/0046 already justify the
cross-family rule as a *guardrail* against same-weights self-judging, and the
C1 measurement showed capability dominates family on absolute anchor scoring.
The one property not yet measured is **correlated error across a candidate
set** (judging a generator's own near-identical candidates). Doing that
properly needs a new instrument (extract the N=3 divergence candidates per
goal, have self vs cross-family judges rank them listwise, and rate the
candidates by hand). It is a follow-up, not a blocker. The existing `pairwise`
anchor protocol is position-biased (27/40 order-inconsistent in C1) and should
not be used to claim discrimination.

## Write-up

Append a "G-F4 closeout" section to `docs/probes/f4-verifier-run.md` (or a new
`docs/probes/gf4-closeout.md`) with: environment digests, P1 table, P2 headline
table, cost-normalized quality, T-a/b/c, and an explicit per-arm verdict. Then
flip Gate G-F4 in `ROADMAP.md` if the arms are met, or name the open arm and
why. Record any ADR-0045/0046 amendment the data supports.

## Known confounds to state honestly

- T-c across fresh jobs confounds model nondeterminism with input
  nondeterminism.
- The anchor's 12 text/document ratings were previously machine-provisional;
  human rows replace them.
- Single-residency means the judge is swapped in at the comparison phases;
  budget for a cold load.
- n=16 anchor, n=10 goals: report effect sizes, not p-values.
