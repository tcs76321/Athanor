# ADR 0135 — v4 is the utility/cycle benchmark

**Status:** Accepted · **Date:** 2026-11-10 ·
**Refs:** [ADR-0075](0075-frozen-corpus-compass.md),
[ADR-0092](0092-benchmark-label-integrity.md),
[ADR-0116](0116-benchmark-portfolio.md),
[ADR-0133](0133-paired-0x-benchmark-v3.md),
[ADR-0134](0134-project-cycles-and-blocks.md), ARCHITECTURE §19.

## Context

ADR-0133 made v3 the 0.x instrument: three arms (single, cascade, full), four
suites, two generators and two judges, with a quality claim gated on judge
agreement and a human sample. v3 did useful work — it produced the paired
report, the mechanism audit, the two-judge scorer, and a real engine fix set —
but the quality smoke failed its own gate (judge agreement 0.17–0.33 at n=4,
against a 0.60 floor), the corpus never reached its freeze counts, and the
product then changed shape.

That change is the reason for v4. The 0.x line is now a **project timeline of
cycles** (ADR-0134): a cycle is a typed block graph, downstream blocks consume
upstream artifacts, and a schedule can run the same template again. v3 measures
one goal → one DAG → one artifact. It cannot see whether the architecture it
was meant to justify actually composes, cites, or repeats.

v4 is therefore a **new instrument**, not an edit to v3. v3 remains in the tree
unchanged for the paired quality experiment if it is revived; its numbers are
not a v4 baseline. v2 remains the frozen historical pilot.

## Decision

### 1. v4 is the 0.0.0 utility instrument

`eval/bench/v4/` measures the utility architecture the product now ships. It is
cut before `v0.0.0-rc1` and frozen through the 0.x series. The headline is
**deterministic**: external checks over the produced artifact, never the
engine's own acceptance or judge.

### 2. Two arms, paired by task

- `oneshot`: one engine task on the task's goal. This is the strong single
  generation the architecture has to beat or match.
- `cycle`: the task's declared built-in template (currently `daily-brief`) on
  the task's declared sources, compiled to a DAG and run by the scheduler.

Generation is unseeded, so pairing is by **task**, not by sample. Intervals are
task-clustered. Repeated runs estimate variance; they do not create paired
samples.

### 3. The `brief` suite is the first claim suite

A v4 brief task declares:

- a goal (≤500 characters, the project goal limit),
- one or more allowlisted source URLs,
- a built-in template,
- deterministic checks: required headings, the declared source present, **no
  URL outside the declared allowlist**, the source cited in every substantive
  paragraph, a refusal phrase ban, and a word bound.

The citation contract is the point. A cycle that fetches a source, extracts it,
and synthesizes a brief must name that source in the artifact; an unsourced
claim or an invented URL is a failure. This is measurable without a judge, and
it is exactly what the block graph is for.

The seed is four tasks with one held out. It grows in place until the freeze;
the held-out slice is never used for prompt tuning.

### 4. The roster is carried over from v3

Two generators (`bonsai-2:27b`, `gemma4:12b-mlx`) and two cross-family judges
(`granite4.2:8b`, `mimo-v2.6:9b-mxfp8`) stay pinned so a v4 delta is
attributable to the architecture, not to a model change. The in-loop judge
remains granite; offline judges are **advisory** and do not gate the headline.
The v3 judge-agreement gate is not a v4 claim gate; a quality claim returns only
with a corpus large enough to test agreement honestly.

### 5. Pre-registered claims

- **Non-inferiority:** `cycle` vs `oneshot` on the brief suite, per generator.
  The task-clustered margin is −5 percentage points; the cycle must not be worse
  beyond it.
- **Utility floor:** the cycle arm passes ≥75% of brief tasks, and ≥75% on the
  citation check specifically.
- **Mechanism coverage:** every declared block of a cycle must run. A completed
  job with a missing block is a capability failure, not a pass.
- **Cost:** the cycle may cost more; it must not silently cost more for a worse
  artifact. The report carries wall time and job count.

### 6. The label comes from the external check

A v4 row is labeled by the deterministic external outcome, not by whether the
engine accepted the artifact (ADR-0092). `engine_accepted` is reported
separately. A judge score is evidence about prose, not proof of correctness.

### 7. One-shot is a real baseline

The oneshot arm runs a single generation with reflection and multi-candidate
divergence disabled. It is the honest "just ask the model once" shape. A cycle
that only ties it is a valid outcome; a cycle that loses is a regression.

## Consequences

- The benchmark now exercises the product's actual authoring surface: template
  → compile → scheduler → artifact.
- Citation discipline and block composition become first-class, deterministic
  metrics.
- The corpus is deliberately small at the seed. Results are directional until
  the freeze; the protocol says so.
- v3's quality machinery stays available but is not on the v4 critical path.
- A v4 number cannot be compared to a v3 number; the arms, corpus, and claim
  differ.

## Alternatives rejected

- **Keep v3 and call the cycle a "full" arm.** v3's arms are engine phase
  shapes, not cycle graphs. Mixing them would make the claim uninterpretable.
- **Judge the briefs.** The contract is citation-shaped and deterministic; a
  judge adds cost and disagreement without adding proof.
- **Start with the whole utility surface.** Build, discovery, memory, and
  scheduling each need different checks and fixtures. The brief suite proves the
  instrument end to end first; the next suite is added in place.
- **Compare across model versions.** The v3 roster is pinned for a reason; a
  generator swap is a new benchmark decision, not a run flag.
