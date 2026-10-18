# ADR 0075 — The frozen benchmark is the controlled study

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
[ADR-0066](0066-release-scope-soak.md) (coverage-based soak),
[ADR-0061](0061-harder-benchmark.md) (harder corpus),
[ADR-0073](0073-soak-validation-harness.md) (soak harness),
[ADR-0076](0076-continuous-evidence.md) (real-use evidence),
[ADR-0078](0078-core-pod-only-runtime.md) (Core Pod); ROADMAP §3 (G7/G8).

## Context

The soak began as a release-scope checkpoint ([ADR-0066](0066-release-scope-soak.md))
and now also carries conformance and benchmark readouts
([ADR-0073](0073-soak-validation-harness.md)). But a **soak measures
endurance**: leaks, crashes, orphans, steady state. Freezing the soak says
nothing about whether a change to the engine — especially the multidimensional
context/memory engine (MCE) — improved real performance.

To attribute a capability change we need a different instrument: a **benchmark**
whose task set, procedure, and **scoring** are fixed, so the system is the only
variable.

Three constraints shape it: a model-driven, multi-hour run cannot live in CI; a
formal release gate would block tags on a human-timed run; and a benchmark that
changes between runs cannot be compared across versions.

## Terms

- **Benchmark** — task set + procedure + external scoring + anchor. The
  instrument.
- **Corpus** — the task set, one part of the benchmark.
- **Matrix** — a capability run shape: tasks × models/variants × approaches ×
  repeats.
- **Soak** — an endurance run shape: one config, long-running, leak/crash/orphan
  and time-triggered checks. It reuses the corpus but is **not** the capability
  instrument.
- **IRL evidence** — real-use selection data ([ADR-0076](0076-continuous-evidence.md)).

## Decision

1. **Not CI/CD, not a formal gate.** The benchmark is the **design compass**; the
   soak is a pre-release **endurance check**. Running either is a discipline,
   not a pipeline stage.
2. **Freeze the benchmark, not just the corpus.** At **x.0.0**, pin all three
   parts and keep them immutable until **(x+1).0.0**:
   - the **task set** (content-hashed), with a held-out slice never used for
     tuning;
   - the **procedure** (run shape, repeats, model/variant registry, anchor);
   - the **external scoring** (the deterministic checks/tests).
   New tasks or changed checks are a **new benchmark version**, cut only at a
   major boundary.
3. **The benchmark must be sensitive to what we change.** For every subsystem we
   intend to measure (MCE/context/memory, the engine loop, verifier, scheduler,
   routing), the corpus must contain tasks where that subsystem is the
   **bottleneck**, and the run must report **mechanism metrics** alongside
   outcomes: retrieval hit/attribution, context tokens and assembly, KV
   pressure/evictions, compaction fidelity, per-phase calls, tokens/cost,
   latency. A benchmark of short tasks cannot detect an MCE change.
4. **Two scoring layers, one headline.** The headline metric uses the **frozen
   external** checks/tests, not the engine's own verifier/judge — otherwise
   improving the verifier would "improve" the score without a capability change.
   The engine's internal accept/reject and judge scores are **reported
   separately** as system behaviour. Raw outputs are retained so historical runs
   can be re-scored against a new benchmark version.
5. **Within-version A/B *and* across-version comparison.** The benchmark runs
   (a) config A/B on the same frozen tasks (e.g. MCE toggles, prompt/context
   policy) to attribute a change, and (b) version-to-version comparison on the
   same benchmark. Same tasks, same procedure, same external checks.
6. **Two run shapes.** The **capability matrix** is the compass (screen on an
   anchor, confirm on the full corpus); the **endurance soak** is the survival
   check. Both measure the shipped **Core Pod**
   ([ADR-0078](0078-core-pod-only-runtime.md)).
7. **The benchmark varies Athanor, not the world.** Models/variants, arms,
   gates, escalation, and routing are the variables; the benchmark is the
   constant.
8. **Two-sided acceptance.** The benchmark must **not regress**, and real-work
   evidence ([ADR-0076](0076-continuous-evidence.md)) must **improve**. Neither
   number alone is a claim.

## Amendment — 2026-10-17: one persona set, a 12-hour slice, and a pinned protocol

The first authoring pass exposed two ambiguities that would have made the
benchmark both unaffordable and under-specified: whether the full run sweeps
models/personas, and what exactly is held constant. This amendment resolves
them.

- **One persona set for the full benchmark.** The headline run uses the five
  canonical roles and their pinned models, temperatures, and context targets. It
  does **not** sweep models or persona variants. The leaderboard/onboarding is a
  separate, short **anchor** screen (12 tuning-only tasks × the pinned model
  roster); it is an aggregation of runs, not a second benchmark.
- **A full slice is one pass, ≤ 12 h.** The 70 deterministic tasks run once on
  the canonical set. `-passes 1` makes "one pass" explicit; the wall-time budget
  is a safety cap. The earlier "full matrix" (arms × repeats × 10 models) is a
  pre-tag confirmation, not the initial slice.
- **The freeze includes the run contract.** `eval/bench/protocol.yaml` pins the
  model tags **and Ollama digests**, persona roles/families/temperatures,
  context targets, inference parameters, embedding model, job-pod and Core Pod
  images, and the run shape. Changing any of them is a new benchmark version.
  `make bench-verify` refuses a run when a digest moves.
- **The vector half is on.** The canonical config sets
  `memory_embedding_model`; leaving it empty would degrade retrieval to FTS-only
  and make the MCE measurement hollow.
- **Anchor tasks are tuning-only.** No held-out task may appear in the anchor;
  otherwise model selection contaminates the held-out slice.

This keeps the controlled study constant across 0.x while making the initial
slice and the onboarding screen fit in a day.

## Amendment — 2026-10-18: v2 is the 0.0.0 instrument

The v1 80-item E/M/H corpus ran as the **pilot**: 56/70 deterministic (80%),
held-out 12/14 (86%), but **code 30/30** and two checks were too literal (a
correct fenced JSON answer and a correct “I won’t” refusal failed bare-JSON /
exact-phrase gates). The probe also scored only accepted artifacts, so a
non-code failure fell back to the engine’s decision.

v2 is cut at **0.0.0** as the hard H/X instrument
([frozen-corpus-v2.md](../frozen-corpus-v2.md)):

- **41 deterministic tasks, all H or X.** No E, no M. The v1 H tasks are the
  floor; the new X tier is repo-scale/multi-file code, long-context synthesis,
  and adversarial reconciliation. Code grows to 18 tasks (6 X).
- **4 memory scripts + 6 human-review tasks**, with 10 deterministic + 3 + 3
  held out.
- **12 H anchor**, single-shot, ≤12h; the full H/X slice is one pass, ≤12h.
- **Fair deterministic checks:** `json_extract` for fenced JSON,
  `must_contain_any` for refusals, and artifact-only synthesis. The probe scores
  the highest-scoring candidate independently of the engine’s accept.
- The canonical persona set, models/digests, context targets, inference, and
  embedding model are unchanged from v1; they are pinned in
  `eval/bench/v2/protocol.yaml`.

v1 remains the pilot baseline. This is a **new version**, not an edit; a change
to v2 is v3 at 1.0.0.

## Consequences

- The current 15-task corpus is **insufficient**: it lacks long-context,
  multi-session/memory, and compaction tasks and reports no mechanism metrics,
  so it cannot attribute an MCE change. It must be expanded before the 0.0.0
  freeze.
- Freezing the external checks is load-bearing: a check is part of the
  instrument, not the system under test.
- The MCE must be runnable with config toggles on the same tasks (a within-version
  A/B), not only as a version-to-version delta.
- Retention of raw outputs per version; plan compaction that keeps the
  decision-relevant signal.
- A **smoke subset** runs on engine-surface changes; the full matrix runs
  pre-tag. No CI wiring, no tag-blocking automation.

## Alternatives rejected

- **Freeze only the soak.** Measures survival, not capability.
- **Freeze tasks but let the grader evolve.** Scores move without a capability
  change.
- **Use the engine's own judge as the headline.** Self-grading: improving the
  judge would look like improving the model.
- **A mutable benchmark.** Destroys cross-version comparability, which is the
  point.
- **A full cross of every dimension.** Hundreds of hours; use screen → confirm
  and a fractional approach-variant set.
