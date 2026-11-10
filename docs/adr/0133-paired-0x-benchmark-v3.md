# ADR 0133 — The 0.x benchmark is paired, multi-model, and quality-first (v3)

**Status:** Accepted · **Date:** 2026-11-10 ·
**Refs:** [ADR-0075](0075-frozen-corpus-compass.md),
[ADR-0092](0092-benchmark-label-integrity.md),
[ADR-0116](0116-benchmark-portfolio.md),
[ADR-0117](0117-quality-monotonicity.md),
[ADR-0118](0118-one-shot-first.md), ARCHITECTURE §19.

## Context

The v2 corpus was a useful pilot, but it cannot support the claims the 0.x
series needs to make:

- It measures an **absolute pass rate** on one canonical model set. It has no
  one-shot comparator, so it cannot show that the loop is non-inferior to a
  single generation, let alone better.
- It does not require the engine to exercise its agentic mechanisms. A task can
  pass without tests being run, a failure being observed, a repair being
  applied, or memory being used.
- It has no quality or creativity measurement. Every task is pass/fail, so a
  deeper, more original, better-structured artifact and a minimal passing one
  score the same.
- Its code tasks are Python-shaped and mostly single-file. The multi-file,
  observe-and-repair, long-horizon, and memory behaviors are not measured.
- The model roster changed. v2 was measured with `ornith-1.5:9b`; the current
  pool's best quality-per-size choices are `bonsai-2:27b` and
  `gemma4:12b-mlx`. A v2 number is not comparable across that change.

v2 stays frozen as a historical pilot. It is not the 0.x instrument.

## Decision

### 1. v3 is the 0.x benchmark

`eval/bench/v3/` is a **new benchmark version**, cut before `v0.0.0-rc1` and
frozen through the 0.x series. v1 and v2 remain in the tree, untouched, as
historical evidence. v3 is not an edit to either; v4 is the 1.0 cut.

The benchmark's headline is no longer a bare pass rate. It is, in priority
order:

1. **Quality/complexity:** the full loop usually produces a higher-quality,
   more creative, more complex artifact than one shot, with equal correctness.
2. **Agentic capability:** on every declared agentic task, the engine actually
   exercises the required mechanism (tests, observation, repair, multi-file
   edits, memory, multi-job).
3. **Non-inferiority:** cascade is never worse than a strong one-shot baseline
   on the declared suites, within a pre-registered margin.
4. **Early stop:** cascade identifies one-shot-sufficient tasks and stops after
   one candidate without a quality loss.
5. **Cost:** the worst case is a tie at a reported overhead, not a silent tax.

### 2. Two generators, two judges, four families

The 0.x instrument pins:

| Role | Model | Family | Why |
|---|---|---|---|
| Generator | `bonsai-2:27b` | bonsai-2 | Best quality-per-GB at the host's size budget; 32k target context |
| Generator | `gemma4:12b-mlx` | gemma | Second independent lineage; slightly higher qualification generator score |
| Judge (in-loop + rubric) | `granite4.2:8b` | granite | 53/56 judge cases (94.6%); cross-family to both generators; fits alongside either |
| Judge (rubric cross-check) | `mimo-v2.6:9b-mxfp8` | mimo-v2.6 | 53/56 (94.6%); fourth-family cross-check. The initial 2B LFM3 judge (50/56) was replaced after the quality smoke. Both absolute-rubric judges disagreed on the 4-task smoke (Spearman 0.17–0.33), so the quality claim is gated on the human anchor and a larger sample, not on a judge swap |

Both generators run the claim-critical suites. The engine's in-loop judge is
`granite4.2:8b` so the generator comparison is not confounded by judge
rotation; both judges score quality artifacts **offline**, blind to provenance.
Deterministic checks remain authoritative (ADR-0092); judges add quality signal
and a reliability check.

### 3. Three arms, paired by task

Every claim suite runs the same tasks under:

- `single`: one candidate, no reflection, no comparison — a strong one-shot
  baseline.
- `cascade`: one-shot-first (ADR-0118): generate one, verify, stop on pass,
  escalate to the full panel on failure.
- `full`: N=3 + reflection + comparison — the quality ceiling.

Generation is unseeded (diversity is the point), so pairing is **by task**, not
by sample. Repeated runs estimate variance; they do not create paired samples.
All intervals are task-clustered.

### 4. Suites

| Suite | n | Passes | Arms | Purpose |
|---|---:|---:|---|---|
| Core | 28 | 1 | single, cascade, full | Breadth across code, data, document, text, adversarial |
| Agentic | 14 | 3 | single, cascade, full | Repair, multi-file, observe-and-repair, long-horizon, memory |
| Quality | 14 | 3 | single, full | Open-ended work with deterministic anchors plus a quality rubric |
| One-shot-sufficient | 8 | 1 | single, cascade | Early-stop rate, precision, and overhead |

**Held out: 16 of 64 (25%)** — 8 core, 4 agentic, 3 quality, 1 one-shot. Held-out
tasks are never used for prompt tuning.

### 5. Pre-registered claims

- **Non-inferiority:** cascade vs single on core + agentic, per generator. The
  task-clustered 95% CI lower bound must be above −5 percentage points, and no
  task may lose two of three runs.
- **Quality:** full vs single on the quality suite, per generator. Mean rubric
  delta ≥ +0.5 on a 1–5 scale, CI lower bound > +0.2, at least 60% of tasks
  improve, correctness is non-inferior, judge agreement ≥ 0.6, and the blind
  human sample agrees on direction in ≥ 70% of pairs.
- **Agentic:** 100% mechanism coverage on every declared family. A pass without
  the required mechanism is a capability failure, not an agentic win.
- **Early stop:** cascade stops after one candidate on ≥ 75% of one-shot-
  sufficient tasks with a non-inferior pass rate, and escalates on ≥ 50% of
  agentic tasks.
- **Complexity** is rewarded only when it serves the brief. The rubric has an
  explicit bloat penalty for gratuitous files, LOC, features, or verbosity.

### 6. Mechanism assertions are part of the benchmark

Each agentic task declares the mechanisms it requires. The runner reads the job
event trace and asserts they occurred. This is what makes "fully agentic" a
test rather than an adjective.

### 7. v3 is frozen at rc1

The corpus, checks, rubric, protocol, and model digests are frozen at
`v0.0.0-rc1`. A defect discovered during a run is reported, not fixed. A v3.1
is cut only when a task is invalid (impossible check, broken fixture), with an
ADR, a new digest, and re-runs of the affected rows.

## Consequences

- The 0.x claim is conditional on two generators and two judges, and says so.
- Quality and creativity become measurable, at the cost of a judge protocol and
  a human sample that must be maintained.
- The run is heavier: four models, three arms, and repeated claim suites. Tiered
  execution (claim suites first, breadth second) keeps it within a two-night
  window.
- v2 numbers remain in the tree for audit, but no new claim compares across v2
  and v3.
- The one-shot-first policy becomes a measured operating shape rather than an
  assumption. A default flip is a separate ADR after the study.

## Alternatives rejected

- **Keep extending v2.** It has no one-shot comparator, no mechanism
  assertions, and no quality measurement; adding those is a new version, not an
  edit.
- **One generator.** A single model makes a quality claim a model anecdote. Two
  independent lineages at the host's size budget give the claim a second leg
  without a full sweep.
- **One judge.** The judge is advisory, but a single judge's preferences become
  the quality definition. Two judges plus a human sample make disagreement
  visible.
- **Judge rotation in-loop.** Rotating the in-loop judge confounds the generator
  comparison and triples judge cost. Judges score offline; the engine keeps one
  stable in-loop judge.
- **Reward artifact size.** More code is not better code. The rubric rewards
  depth and appropriate complexity, and penalizes bloat.
