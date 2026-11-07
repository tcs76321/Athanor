# ADR 0129 — Qualification category taxonomy and balanced planning

**Status:** Accepted · **Date:** 2026-11-07 · **Refs:** ADR-0049, ADR-0075,
ADR-0104; `internal/onboard`, `internal/composition`, `eval/qualification`,
`eval/diagnostic`

## Context

Qualification started as a small capability screen (code, data, document, text,
media, general, judgment). Athanor's real internal work is broader: it repairs
candidates, reads long context, chooses engine operations, plans dependency
graphs, and must know when it does not know. Those are separate jobs with
separate failure modes, and a model that is good at writing prose may be poor
at them. Ranking them inside "general" hid the distinction.

At the same time, a first-use screen that runs every case for every model can
starve later models when it hits a wall-clock bound, and one that switches
models per case pays a large load/unload cost.

## Decision

### 1. Categories are first-class

The capability vocabulary adds:

| Capability | What it measures |
|---|---|
| `repair` | Minimal fixes, observing the real failure, preserving the public API |
| `longctx` | Exact retrieval and aggregation across distracting context |
| `operation` | Choosing engine-executed operations; no model-invented tool calls |
| `planning` | Dependency order, parallelizable steps, infeasibility, replanning |
| `calibration` | Unknown/UNSUPPORTED/ASK/CONFLICT instead of fabrication |

`summarize`, `security`, `agentry`, `instruction`, and `grounding` remain
separate from the original set. Each models in `internal/composition` and is
recorded from deterministic qualification cases.

### 2. Deterministic checks, no judge during qualification

Generators use exact, JSON, or contains+word-bound checks; judges use fixed
good/bad pairs. `contains` adds `must_contain`, `must_not_contain`, and word
bounds, so compaction, safety verdicts, long-context recall, and calibration
can be checked without a judge. The benchmark remains the external scorer.

### 3. Balanced, open-ended planning

- A **core** of one case per capability runs for every model first.
- Extra cases run in fixed-size **blocks**; a block runs for every model or not
  at all.
- There is no hard wall cutoff in the UI or the default CLI ladder; the run is
  resumable and cancelable.
- Scores use the **intersection** of cases completed by every model. An
  interrupted block cannot advantage the models that ran first.

The block size (30) is the explicit trade-off between model-switch cost and
equal coverage.

### 4. Provenance and benchmark linkage

`eval/qualification/categories.yaml` maps each category to real frozen v2 or
non-frozen diagnostic task ids; a test fails on drift. The real benchmark
subset is `eval/bench/v2/anchor.yaml` (12 tuning-only H tasks), run on the Core
Pod with `make bench-anchor-core`, which writes standard benchmark results.
Qualification is the cheap prior; the anchor is the benchmark evidence.

## Consequences

- The Models page shows the internal families separately, and role scores use
  them (wide: long-context/summarize/grounding; main: repair/operation;
  tall: planning/calibration/security).
- The default screen is 80 cases (40 generators + 40 judges); deep adds 39 more
  (119 total). The run is longer but no longer truncates unevenly.
- No frozen corpus is edited. New category tasks live in `eval/diagnostic` and
  can be promoted into a future frozen version.
