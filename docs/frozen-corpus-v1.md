# Frozen benchmark v1 — task set, protocol, and scoring

**Status:** proposed (authoring not started) · **Date:** 2026-10-17 · **Refs:**
[ADR-0075](adr/0075-frozen-corpus-compass.md) (frozen benchmark),
[ADR-0061](adr/0061-harder-benchmark.md) (harder benchmark),
[ADR-0070](adr/0070-declared-checks.md) (declared checks),
[ADR-0078](adr/0078-core-pod-only-runtime.md) (Core Pod); `eval/bench/`.

This spec defines the controlled study ADR-0075 calls for. The **benchmark** is
three frozen things — the **task set**, the **procedure**, and the **external
scoring** — pinned at x.0.0 and unchanged until (x+1).0.0. The **soak** is one
run shape of it (endurance); the **matrix** is the other (capability). It
replaces the current 15-task `eval/bench/tasks.yaml` as the fixed reference.

The benchmark exists to answer, on the same tasks and the same external checks:
**did a change to the system — especially the multidimensional context/memory
engine (MCE) — change real performance?** A task set of short, context-free
prompts cannot answer that.

## 1. Immutability

- The benchmark is **cut at every major release**. At **x.0.0** the task set,
  procedure, and external scoring are content-pinned and **never edited** until
  **(x+1).0.0**.
- v1 is the **0.0.0** benchmark: SHA-256 over `eval/bench/tasks.yaml` plus every
  fixture tree, recorded in `eval/bench/CORPUS.digest`.
- New or corrected tasks, a changed check, or a changed procedure are a **new
  benchmark version** (v2), cut only at a major boundary; v1 stays runnable.
- The digest is checked by a small `make corpus-check` target (discipline, not
  CI).

## 2. Composition

**40 core tasks** (the comparable headline), plus **3 memory scripts** and **4
human-review tasks** (below).

| Archetype | E | M | H | total | of which context-heavy |
|---|---|---|---|---|---|
| code | 2 | 6 | 8 | 16 | ≥ 6 repo-scale / multi-file |
| document | 2 | 3 | 3 | 8 | ≥ 3 long-document (source > window) |
| text | 1 | 3 | 2 | 6 | ≥ 1 long-source rewrite |
| data | 1 | 2 | 1 | 4 | — |
| adversarial | 0 | 1 | 3 | 4 | — |
| odd / easily verified | 1 | 1 | 0 | 2 | — |
| **total** | **7** | **16** | **17** | **40** | **≥ 12** |

- **Code is 40%** because its deterministic tests are the strongest grounded
  signal; half of it is repo-scale so MCE/retrieval actually matters.
- **Odd-but-easily-verified** tasks (exact formatting, units, ordering) are
  cheap, discriminating, and catch harness bugs.
- **Difficulty:** E ~18%, M ~40%, H ~42%, with **headroom evidence** (an H task
  must fail a reference model, or it is really M).

### Memory scripts (3, outside the 40)

A memory script is a **sequence**, not a single job: job 1 → a correction/insight
→ job 2, scored on whether job 2 recalls and applies the earlier input. These are
the only tasks that can show a multi-session memory change.

### Human-review tier (4, outside the 40)

Large, nuanced tasks (a small game, a multi-file refactor, a long brief) scored
by the operator review queue, not deterministic checks. Kept small because human
time is the scarce resource.

## 3. Procedure (part of the freeze)

- **Run shape.** A capability **matrix**: tasks × model variants × selected
  approach variants × repeats. Screen every model/variant on an **anchor**
  (8–12 stratified tasks, 1–2 repeats); confirm the top few on the full set
  (3 repeats). Never full-cross every dimension.
- **Model order.** Model-outer for throughput (one load per model), with a
  randomized task order and a **daily reference-model control** to detect drift.
  A small anchor may be run paired (A/B back-to-back) for tight comparisons.
- **Runtime.** The **Core Pod** (ADR-0078). Record load/switch time and
  cold-start latency.
- **A/B within a version.** The same frozen tasks run under config toggles (e.g.
  `seed_active_chunk`, index limits, compaction on/off, prompt/context policy)
  so a change is attributable, not only a version-to-version delta.

## 4. Scoring (part of the freeze)

- **Headline = frozen external checks/tests.** Deterministic ground truth, not
  the engine's own verifier or judge. Otherwise improving the verifier would
  "improve" the score without a capability change.
- **Internal decisions reported separately**: the engine's accept/reject, judge
  score, and verification path are system behaviour, not the headline.
- **Per-stratum, per-type reporting** (archetype × tier × context demand ×
  verification type); no single aggregate that hides the MCE.
- **Raw outputs retained** (artifacts, events, verdicts, costs) so a future
  benchmark version can re-score an old run.

## 5. Mechanism metrics (MCE sensitivity)

Every run records, alongside the outcome:

| Signal | Source |
|---|---|
| retrieval hit / attribution (did the required chunk appear?) | `context_*`, `dormant_index_*`, `chunk_swapped` events |
| context tokens and assembly / evicted tiers | `kv_cache_assembled`, `kv_cache_evicted` |
| KV pressure / floor violations | `kv_cache_pressure`, `context_floor_violation` |
| compaction fidelity (byte-exact + post-compaction answer) | compaction store + checks |
| per-phase calls / tokens / latency | `llm_call`, `strategy_outcomes` |
| model load / switch time, cold first-token | run harness |

A delta on the headline without a mechanism metric is a result to explain, not
a win to bank.

## 6. Tiers and held-out

- **E** — sanity; a competent model should pass. Guards harness regressions.
- **M** — discriminating; separates model tiers.
- **H** — headroom; a 9–12 B should fail a meaningful fraction.
- **Held-out slice**: **10 of 40** (stratified) marked `held_out: true`. They run
  like any other task but are **never used to tune prompts, routing, thresholds,
  or the escalation ladder**. They exist so a reported improvement has an
  uncontaminated check.

## 7. Per-item requirements (authoring checklist)

1. One clear goal with an explicit, bounded deliverable.
2. Deterministic acceptance (a `test_command` or a `checks:` map), never
   judge-only.
3. A self-contained fixture checked into the repo (no network, no absolute
   paths, no runtime-generated inputs).
4. No network and no external services.
5. A tier justification and, for H, headroom evidence.
6. Expected failure modes (the trap it tests).
7. A quality rubric for the human anchor / advisory judge.
8. A runtime bound.
9. **A context-demand tag** (`short` / `long` / `repo` / `memory`) so the
   benchmark can report MCE-sensitive strata.
10. A contamination check (not in the anchor, qualification suite, or any tuning
    corpus).
11. A stable name (`code-…`, `doc-…`, `text-…`, `data-…`, `adversarial-…`,
    `odd-…`, `mem-…`).

## 8. Smoke subset

A fixed **6-item** subset for engine-surface changes: two H code (one
repo-scale), one M code, one H document (long), one H text, one H data. It
finishes in ~30 min on the reference host and catches the common regressions
(candidate parsing, verdict parsing, checks, pod execution, context assembly,
scheduling).

## 9. How it is used

- **Smoke** after any engine-surface change; record a one-paragraph delta plus
  the relevant mechanism metrics.
- **Full matrix** before a tag; compare against the previous tag on the same
  benchmark version.
- **A/B** a config change (e.g. MCE) on the same frozen tasks before shipping it.
- Run against the **Core Pod**, with the state volume readable by the probe.
- **Never** as a CI job or tag blocker; **never** tune on the held-out slice.

## 10. Open items

- Author the 25 new core tasks and the 7 extra items (3 memory scripts, 4 human).
- Tag context demand and mark the 10 held-out; record the digest.
- `make corpus-check` (digest) and a smoke-subset selector.
- Implement the mechanism-metric reducer over the run's event log.
- Validate the runtime budget on the reference host before freezing.
