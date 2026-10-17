# Frozen benchmark v1 — task set, protocol, and scoring

**Status:** frozen (v1, cut 2026-10-17) · **Refs:**
[ADR-0075](adr/0075-frozen-corpus-compass.md) (frozen benchmark),
[ADR-0061](adr/0061-harder-benchmark.md) (harder benchmark),
[ADR-0070](adr/0070-declared-checks.md) (declared checks),
[ADR-0078](adr/0078-core-pod-only-runtime.md) (Core Pod); `eval/bench/`.

This spec defines the controlled study ADR-0075 calls for. The **benchmark** is
three frozen things — the **task set**, the **procedure**, and the **external
scoring** — pinned at x.0.0 and unchanged until (x+1).0.0. The **soak** is one
run shape of it (endurance); the **matrix** is the other (capability). It
replaces the earlier 15-task corpus as the fixed reference; the frozen set is
the 80 items in `eval/bench/`.

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
- The digest is checked by `make corpus-check` (discipline, not CI); it covers
  `tasks.yaml`, `memory-scripts.yaml`, `human-review.yaml`, and every fixture.

## 2. Composition

**70 deterministic tasks** (the comparable headline), plus **4 memory scripts**
and **6 human-review tasks** (below).

| Archetype | E | M | H | total | context-heavy |
|---|---|---|---|---|---|
| code | 6 | 12 | 12 | 30 | 1 repo-backed |
| document | 3 | 8 | 5 | 16 | 3 long-source, 1 repo-backed |
| text | 3 | 6 | 3 | 12 | 1 long-source |
| data | 1 | 3 | 2 | 6 | 2 long-input |
| adversarial | 0 | 2 | 4 | 6 | — |
| **total** | **13** | **31** | **26** | **70** | |

- **Code is 38% of all 80 items** (43% of deterministic) because its tests are
  the strongest grounded signal. It is mostly self-contained; the one
  repo-backed task (`code-refactor-provided`) is the MCE retrieval probe.
- **Odd-but-easily-verified** is a **tag** (`category: odd`) on five tasks
  across code and text, not a separate archetype: exact formatting, units,
  ordering. Cheap, discriminating, and they catch harness bugs.
- **Difficulty:** E ~19%, M ~44%, H ~37%, with headroom evidence (an H task
  must fail a reference model, or it is really M).
- **Held-out:** **20 of 80 (25%)** — 14 deterministic + 3 memory + 3 human,
  stratified; never used for tuning.

### Memory scripts (4, outside the 70)

A memory script is a **sequence**, not a single job: job 1 → a correction or
insight → job 2, scored on whether job 2 recalls and applies the earlier input.
These are the only tasks that can show a multi-session memory change. Three are
held out. **Status:** the three correction/insight scripts run via
`make bench-memory`; the compaction script is partial (the multi-turn filler
driver is pending).

### Human-review tier (6, outside the 70)

Nuanced tasks (a ship-or-wait memo, a design review, a tone rewrite, a database
tradeoff, an incident update, a code review) scored by the operator review
queue, not deterministic checks. Reported separately as a calibration set; three
are held out.

## 2a. Canonical run protocol (part of the freeze)

The task set is only half the freeze. `eval/bench/protocol.yaml` pins the **run
contract**:

- **One canonical persona set** for the full run: the five roles (`wide`,
  `tall`, `main`, `security`, `alternative`), their models and families, role
  temperatures, context targets (32768; `tall` 16384), inference parameters,
  embedding model, job-pod image, and runtime image. The full run does **not**
  sweep models or persona variants.
- **Full slice = one pass.** The full run is one complete pass over the 70
  deterministic tasks on the canonical set, budgeted at **≤ 12 h** of machine
  time (one pass is ~3–5 h on the 9B reference, so the budget is a cap, not a
  target). `-passes 1` makes this explicit. For a capped run the budget is a
  hard cap checked between tasks; an in-flight job can overshoot by at most its
  per-job timeout, and that run is marked incomplete rather than overrunning.
- **Anchor = the short onboarding/leaderboard screen.** Twelve tuning-only tasks
  (`eval/bench/anchor.yaml`), one **single-shot** pass per pinned roster model,
  **≤ 12 h total**. The anchor is not the headline; it selects models for
  onboarding and orders the leaderboard. The anchor uses the same role models,
  temperatures, and context targets; only the arm differs (`single` vs `full`).
- **The leaderboard is an aggregation, not a second benchmark.** It merges the
  anchor runs into one row per model using the same deterministic checks. The
  full benchmark is then run on the chosen canonical persona set.
- **Models are pinned by digest.** The roster in `protocol.yaml` records the
  Ollama digest of every model; `make bench-verify` refuses a run when a tag has
  moved. The embedding model (`nomic-embed-text`) is pinned too — leaving
  `memory_embedding_model` empty would silently make retrieval FTS-only and
  hollow out the MCE measurement.
- **Changing any pinned field is a new benchmark version (v2), not an edit.**
  This includes a model digest, a role temperature, a context target, the
  embedding model, the inference engine version, the host class, the arm, the
  pass count, or the scoring rule. The task digest and the protocol are verified
  together by `make corpus-check`.

The initial full slice is the **70 deterministic tasks**; the 4 memory scripts
join it when the sequence runner lands, and the 6 human-review tasks run
asynchronously (operator time, not machine time). Both are part of the frozen
set, not edits to it.

## 3. Procedure (part of the freeze)

- **Run shape.** Two shapes share one frozen task set. **Full:** the canonical
  persona set from `protocol.yaml`, one pass over all 70 deterministic tasks,
  **≤ 12 h** (typically 3–5 h on the 9B reference). **Anchor:** the 12-task
  tuning-only subset, one **single-shot** pass per pinned roster model,
  **≤ 12 h total**; this is the onboarding/leaderboard screen. A full matrix (multiple arms × repeats) is
  a pre-tag confirmation, not the initial slice and not the leaderboard.
- **One persona set.** The full run uses the five roles, models, temperatures,
  context targets, inference parameters, embedding model, and runtime image
  pinned in `protocol.yaml`. The leaderboard varies only the generator model;
  the role shape and judge stay identical so a delta is attributable.
- **Model order.** Model-outer for throughput (one load per model), with a
  randomized task order and a **daily reference-model control** to detect drift.
  A small anchor may be run paired (A/B back-to-back) for tight comparisons.
- **Runtime.** The **Core Pod** (ADR-0078). The probe runs **inside** the Core
  Pod, reads the named state volume, and writes the report to a host-mounted
  `/bench` (`scripts/bench-core.sh`, `make bench-full`). The host binary is a
  development path only; it is not the benchmark runtime. Record load/switch
  time and cold-start latency.
- **A/B within a version.** The same frozen tasks run under config toggles (e.g.
  `seed_active_chunk`, index limits, compaction on/off, prompt/context policy)
  so a change is attributable, not only a version-to-version delta. A/B
  toggles are reported separately from the headline and never change the
  pinned protocol for the headline run.

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
- **Held-out slice**: **20 of 80** (25%; 14 deterministic + 3 memory + 3
  human), stratified by archetype, tier, and context demand. They run like any
  other task but are **never used to tune prompts, routing, thresholds, or the
  escalation ladder**. They exist so a reported improvement has an
  uncontaminated check. The onboarding/leaderboard anchor is drawn **only from
  the tuning slice**; a held-out task in the anchor would contaminate it.

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
11. A stable name (`code-…`, `doc-…`, `text-…`, `data-…`, `adv-…`,
    `adversarial-…`, `mem-…`) and an optional `category` tag (`adversarial`,
    `odd`).

## 8. Smoke subset

A fixed **6-item** subset for engine-surface changes: `code-csv-rfc4180`
(H code), `code-refactor-provided` (M repo-backed), `doc-cited-brief` (H
document, long source), `text-level-rewrite` (H text, long source),
`data-json-normalize` (H data, long input), `adv-h-contradictory-requirements`
(H adversarial). It finishes in ~30–40 min on the reference host and catches the
common regressions (candidate parsing, verdict parsing, checks, pod execution,
context assembly, scheduling).

Run it with `make soak-smoke` (it sets `MINI_SOAK_ONLY` and a fresh output dir).

## 9. How it is used

- **Smoke** after any engine-surface change; record a one-paragraph delta plus
  the relevant mechanism metrics.
- **Full matrix** before a tag; compare against the previous tag on the same
  benchmark version.
- **A/B** a config change (e.g. MCE) on the same frozen tasks before shipping it.
- Run against the **Core Pod**, with the state volume readable by the probe.
- **Never** as a CI job or tag blocker; **never** tune on the held-out slice.

## 10. Open items

- ~~Author the 70 deterministic tasks, 4 memory scripts, and 6 human-review
  tasks.~~ **Done.** `eval/bench/`.
- ~~Tag context demand, mark the 20 held-out, and record the digest.~~ **Done**
  (`CORPUS.digest`, `make corpus-check`).
- ~~Validate the code checks against reference solutions.~~ **Done**
  (`reference.yaml`, `TestBenchCodeReferences`).
- **Memory-script sequence runner:** `probe memory` runs the three
  correction/insight scripts (one project, correction/insight, later job,
  injection check). The compaction script's multi-turn filler driver is pending.
- **Human-review queue** (the 6 rubric tasks are specified; the operator UI is
  runner work).
- **Anchor pairs** (good + near-miss artifacts) for judge-discrimination, using
  each task's `quality_rubric` and `expected_failure_modes` as the brief.
- Implement the mechanism-metric reducer over the run's event log.
- Run the first **≤12 h full slice** on the reference host (`make bench-full`)
  and confirm the wall time, the per-job distribution, and the 12 h cap.
- Add the **Core Pod run mode** to the probe: the protocol pins
  `athanor-core:0.0.0`, but the probe still starts the host binary. The benchmark
  runtime is the Core Pod (ADR-0078); the host path is a development mode.
