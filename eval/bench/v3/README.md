# Athanor benchmark v3 (0.x) — paired, multi-model, quality-first

**Status:** draft · **Cut:** 2026-11-10 · **Freeze:** `v0.0.0-rc1` ·
**Decision:** [ADR-0133](../../../docs/adr/0133-paired-0x-benchmark-v3.md)

v3 is the **0.x instrument**. It is a new benchmark version, not an edit to
v1 or v2; both remain frozen historical pilots. v4 is the 1.0 cut.

The headline is not a bare pass rate. It is, in priority order:

1. **Quality/complexity** — the full loop usually produces a higher-quality,
   more creative, more complex artifact than one shot, with equal correctness.
2. **Agentic capability** — every declared agentic task actually exercises the
   required mechanism (tests, observation, repair, multi-file edits, memory,
   multi-job).
3. **Non-inferiority** — cascade is never worse than a strong one-shot
   baseline on the declared suites, within a pre-registered margin.
4. **Early stop** — cascade recognizes one-shot-sufficient tasks and stops
   after one candidate without a quality loss.
5. **Cost** — the worst case is a tie at a reported overhead.

## Model matrix

| Role | Label | Model | Family | Digest |
|---|---|---|---|---|
| Generator | `bonsai27b` | `bonsai-2:27b` | bonsai-2 | `f3c4f420…` |
| Generator | `gemma12b` | `gemma4:12b-mlx` | gemma | `117d0d84…` |
| Judge (in-loop + rubric) | `granite8b` | `granite4.2:8b` | granite | `f586c02f…` |
| Judge (rubric cross-check) | `lfm3_2b` | `lfm3-omni:2b-mxfp8` | lfm3-omni | `68366614…` |

All four are distinct families. The engine's in-loop judge is `granite4.2:8b`
for both generators; both judges score quality artifacts offline and blind.
Deterministic checks are authoritative (ADR-0092).

## Composition

| Suite | n | Passes | Arms | Purpose |
|---|---:|---:|---|---|
| Core | 28 | 1 | single, cascade, full | Breadth: code, data, document, text, adversarial |
| Agentic | 14 | 3 | single, cascade, full | Repair (4), multi-file (3), observe-and-repair (3), long-horizon (2), memory (2) |
| Quality | 14 | 3 | single, full | Open-ended work with deterministic anchors + rubric |
| One-shot-sufficient | 8 | 1 | single, cascade | Early-stop rate, precision, overhead |
| **Total** | **64** | | | **16 held out (25%)** |

The claim-critical suites (agentic + quality) run three independent protocol
invocations; breadth runs one. Tiered execution keeps the matrix within a
two-night window.

## Task schema

Every task is a YAML entry with deterministic checks and, for agentic/quality
tasks, declared mechanisms and a rubric:

```yaml
- id: ag-repair-multifile-01
  title: Fix failing tests across the parser and CLI
  suite: agentic
  capability: code
  tier: X
  language: python
  goal: >
    Fix the failing tests in the provided repository without modifying tests.
  repo: eval/bench/v3/fixtures/ag-repair-multifile-01
  test_command: |
    cd "$REPO" && pytest -q
  checks:
    files_unchanged: [tests/]
  requires: [run_tests, observe_failure, edit_multiple_files, repair]
  one_shot_expected: fail
  held_out: false
  quality_rubric: quality-v1
  expected_failure_modes:
    - patches the test instead of the code
    - fixes one module and breaks the other
```

Rules:

- `checks` run **outside** the engine's verifier (ADR-0092). Engine acceptance is
  reported separately.
- `requires` is asserted against the job event trace. A pass without the
  required mechanisms is a capability failure, not an agentic win.
- `one_shot_expected` is a design intent, not a scoring input. Saturation is
  reported, not deleted.
- `held_out: true` tasks are never used for prompt tuning.
- Code tasks ship a reference under `reference/`; the corpus test runs it.
- Quality tasks declare a rubric and deterministic anchors; the rubric is
  scored blind by two judges plus a human sample.

## Arms

| Arm | Behavior |
|---|---|
| `single` | One candidate, no reflection, no comparison — a strong baseline |
| `cascade` | One-shot-first (ADR-0118): generate one, verify, stop on pass, escalate on failure |
| `full` | N=3 + reflection + comparison — the quality ceiling |

Generation is unseeded; pairing is **by task**, not by sample. All intervals
are task-clustered.

## Pre-registered claims

- **Non-inferiority:** cascade vs single on core + agentic, per generator;
  task-clustered 95% CI lower bound > −5 points; no task loses two of three
  runs.
- **Quality:** full vs single on the quality suite, per generator; mean rubric
  delta ≥ +0.5, CI lower > +0.2, ≥60% of tasks improve, correctness
  non-inferior, judge agreement ≥ 0.6, human direction agreement ≥ 70%.
- **Agentic:** 100% mechanism coverage per declared family.
- **Early stop:** ≥75% stop rate on one-shot tasks with non-inferior pass rate;
  ≥50% escalation on agentic tasks.
- **Complexity:** rewarded only when it serves the brief; the rubric penalizes
  gratuitous files, LOC, features, and verbosity.

## Run

```bash
# Verify the pinned models and digests.
make bench-v3-verify

# Run one suite on one generator and one arm (Core Pod, fresh state volume).
BENCH_V3_SUITE=agentic BENCH_V3_GENERATOR=bonsai27b BENCH_V3_ARM=cascade make bench-v3-run

# Run all three arms for a suite (single, cascade, full), sequentially.
BENCH_V3_SUITE=agentic BENCH_V3_GENERATOR=bonsai27b make bench-v3-suite

# Join every arm under BENCH_V3_OUT and write paired.json / paired.md.
make bench-v3-paired-report

# Score the quality suite artifacts with both pinned judges (offline).
make bench-v3-quality

# Compare full vs single rubric scores with a clustered interval.
make bench-v3-quality-report

# The four suites are core, agentic, quality, and one-shot. Repeat the
# generator for gemma12b. The quality suite uses single and full only.
```

The overnight orchestration is tiered: agentic + quality on both generators
first, then core and one-shot. `bench-v3-suite` is a convenience loop, not the
claim-critical scheduler.

## Files

| Path | Contents |
|---|---|
| `protocol.yaml` | run contract: matrix, suites, claims, thresholds |
| `core.yaml` | 28 breadth tasks |
| `agentic.yaml` | 14 mechanism-required tasks |
| `quality.yaml` | 14 rubric-scored tasks |
| `one-shot.yaml` | 8 early-stop tasks |
| `anchor.yaml` | 12-task quick screen (tuning-only, core subset) |
| `memory-scripts.yaml` | memory/correction sequences |
| `fixtures/` | starter repositories and long sources |
| `reference/` | reference solutions for code tasks |
| `CORPUS.digest` | frozen at `v0.0.0-rc1` |
