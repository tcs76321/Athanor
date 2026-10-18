# Athanor benchmark v2 (0.0.0) — the hard H/X corpus

**41 deterministic tasks, all H or X. No E. No M.** Plus 4 memory scripts and
6 human-review tasks. This is a **new benchmark version**, not an edit to v1;
the v1 pilot is at [`eval/bench/`](../) and its findings are in
[`docs/bench-v1-findings.md`](../../../docs/bench-v1-findings.md).

The v1 pilot saturated on code (30/30) and exposed two unfair checks. v2 removes
the easy tier, keeps the v1 H tasks as the floor, and adds an **X** tier above
it: repo-scale/multi-file code, bug hunts, interpreters, merge/pool/migration
tasks, hard data reconciliation, and long-context/adversarial synthesis.

## Composition

| archetype | H | X | total |
|---|---:|---:|---:|
| code | 12 | 6 | 18 |
| document | 9 | 4 | 13 |
| text | 3 | 2 | 5 |
| data | 2 | 3 | 5 |
| **total** | **26** | **15** | **41** |

The document count includes 6 adversarial tasks (`category: adversarial`): 4 H
and 2 X. Code is 44% of the deterministic corpus and the X code is the largest
task class, because code was the saturated archetype in v1.

## What changed from v1

- **No easy tier.** Every E and M task is removed. The v1 H tasks are the
  floor; the new X tasks are harder than any v1 H task.
- **Repo-scale/X code.** `x-code-repo-ratelimit` and
  `x-code-bugfix-pipeline` ship starter packages; the interpreter, merge,
  thread-pool, and SQLite-migration tasks are multi-file/hidden-edge-case work.
- **Fair data checks.** `json_extract` accepts a correct JSON value inside a
  fenced block or a short wrapper; it is not a free pass (the array/schema/field
  checks still apply).
- **Fair refusal checks.** `must_contain_any` accepts `cannot`, `can't`,
  `won't`, `will not`, `unable`, or `not able`.
- **External scoring.** The probe scores the **highest-scoring proposal**
  independently of the engine's accept (`bestCandidateArtifact`), so a
  judge/verifier rejection cannot turn a check-passing artifact into a failure.
- **Artifact-only synthesis.** A `<tool_call>` or planning trace in a text
  artifact is a failure; the v2 human rubric and checks call this out.

## Held-out and anchor

- **Held-out:** 10 of 41 (24%) deterministic + 3 memory + 3 human = 16 of 51
  (31%). Never used for tuning.
- **Anchor:** 12 tuning-only **H** tasks in [`anchor.yaml`](anchor.yaml),
  single-shot, one pass per pinned model, ≤12h total. It is the onboarding /
  leaderboard screen, not the headline.
- **Full slice:** one pass over 41 H/X tasks, one canonical persona set, ≤12h
  (typically 2–4h). `make bench-full` with `BENCH_DIR=eval/bench/v2`.

## Reference validation

Every code task ships a reference under [`reference/`](reference/); the v2 test
copies the fixture (if any) plus the reference into a temp dir and runs the
task's real `test_command`. A broken reference fails the corpus test, not a
model run.

## Files

| path | contents |
|---|---|
| [`tasks.yaml`](tasks.yaml) | 41 deterministic H/X tasks |
| [`x-tasks.yaml`](x-tasks.yaml) | the 15 X definitions (merged into tasks.yaml) |
| [`memory-scripts.yaml`](memory-scripts.yaml) | 4 multi-job memory scripts |
| [`human-review.yaml`](human-review.yaml) | 6 rubric-scored tasks |
| [`protocol.yaml`](protocol.yaml) | v2 run contract (personas, models, run shape) |
| [`anchor.yaml`](anchor.yaml) | 12 H onboarding/leaderboard tasks |
| [`reference/`](reference/) | reference solutions for all 18 code tasks |
| [`fixtures/`](fixtures/) | starter repos, long sources, data schemas |

## Run

```bash
make bench-verify        # model digests from the v2 protocol
make corpus-check-v2     # v2 task/fixture digest
make bench-full          # Core Pod, one pass, BENCH_DIR=eval/bench/v2
make bench-memory        # the 4 memory sequences
```

The v1 targets still work by setting `BENCH_DIR=eval/bench`.

## Known limits

- The v2 full slice has not been measured yet; the 12h cap is the design bound.
- The compaction memory script is still partial (multi-turn filler pending).
- The human-review queue and anchor pairs are still runner/authoring work.
- Code is 44% of the deterministic corpus; a model that saturates v2 code again
  would force a v3 with a new code ceiling, not an edit.
