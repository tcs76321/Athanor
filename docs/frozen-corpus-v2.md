# Frozen benchmark v2 (0.0.0) — the hard H/X corpus

**Status:** frozen (v2, cut 2026-10-18) · **Refs:**
[ADR-0075](adr/0075-frozen-corpus-compass.md) (frozen benchmark, amendment),
[bench-v1-findings.md](bench-v1-findings.md) (the pilot audit),
[`eval/bench/v2/`](../eval/bench/v2/) (the corpus).

v2 is the **0.0.0 benchmark**. The v1 corpus at `eval/bench/` remains the
**pilot** baseline; it is not edited and its report/state are retained.

## Composition

**41 deterministic tasks, all H or X. No E, no M.** Plus 4 memory scripts and
6 human-review tasks.

| archetype | H | X | total |
|---|---:|---:|---:|
| code | 12 | 6 | 18 |
| document | 9 | 4 | 13 |
| text | 3 | 2 | 5 |
| data | 2 | 3 | 5 |
| **total** | **26** | **15** | **41** |

The document count includes 6 adversarial tasks (4 H, 2 X). Code is 44% of the
deterministic corpus; the X code includes repo-scale packages, a bug hunt, an
interpreter, a three-way merge, a thread pool, and a SQLite migration runner.

## Why a v2 now

The v1 pilot (70 deterministic) ran in 64 minutes on the canonical 9B persona
set: **56/70 (80%)**, held-out **12/14 (86%)**, but **code 30/30**. The audit
([bench-v1-findings.md](bench-v1-findings.md)) found three problems:

1. **Code saturation** — v1 has no code headroom for the reference model.
2. **Too-literal checks** — a correct fenced JSON answer and a semantically
   correct refusal (“I won’t”) failed bare-JSON / exact-phrase checks.
3. **External-scoring gap** — the probe only scored accepted artifacts, so a
   non-code failure fell back to the engine’s decision.

v2 removes the easy tier, raises the code ceiling, and fixes the checks and the
scoring. v1 is a valid pilot; v2 is the instrument.

## Frozen protocol (part of the freeze)

`eval/bench/v2/protocol.yaml` pins the same **canonical persona set** as v1
(five roles, model tags and Ollama digests, context 32k/16k, inference
parameters, embedding model, job-pod/Core Pod images, host class) and adds the
v2 task digest and run shapes:

- **Full slice:** one pass over the 41 H/X tasks, one canonical persona set,
  **≤12 h** (typically 2–4 h).
- **Anchor:** 12 tuning-only **H** tasks, single-shot, one pass per pinned
  model, **≤12 h**; onboarding/leaderboard screen, not the headline.
- Changing a model, digest, persona, context target, embedding model, arm, pass
  count, or scoring rule is a **v3**, not an edit.

`make corpus-check-v2` verifies the v2 task+fixture digest. `make bench-verify`
checks the model digests against the v2 protocol.

## Fairness changes

- **`json_extract`** (probe check): a correct JSON value inside a fenced block
  or short wrapper is scored; the schema/array/field checks still apply.
- **`must_contain_any`** (probe check): a refusal may say `cannot`, `can't`,
  `won't`, `will not`, `unable`, or `not able`.
- **External candidate scoring:** `bestCandidateArtifact` scores the
  highest-scoring proposal with the task’s own checks even when the engine
  accepted nothing. The engine’s judge/verifier remains a separate, reported
  decision.
- **Artifact-only synthesis:** a tool-call/planning trace in a text artifact is
  a failure; the v2 text/document tasks and human rubric treat it as such.

## Reference validation

Every v2 code task has a reference under `eval/bench/v2/reference/<id>/`, and
the v2 test copies the fixture (if any) + reference into a temp dir and runs the
task’s real test command. A broken reference fails the test, not a model run.

## Held-out and anchor

- **Held-out:** 10 of 41 (24%) deterministic + 3 memory + 3 human = 16 of 51
  (31%). Never used for tuning.
- **Anchor:** 12 H tasks in `eval/bench/v2/anchor.yaml`; all tuning-only.
- The v1 anchor/pilot is not used as the v2 headline.

## Run

```bash
make corpus-check-v2     # v2 digest
make bench-verify        # v2 model digests
make bench-full          # Core Pod, one pass, BENCH_DIR=eval/bench/v2
make bench-memory        # the 4 memory sequences
```

`make bench-full BENCH_DIR=eval/bench` runs the v1 pilot for comparison.

## Freeze and disposition

- v2 is cut at **0.0.0** and never edited until **1.0.0**. A change is v3.
- v1 stays runnable as the pilot; its findings, report, and state are retained.
- The 10 held-out tasks are frozen; the anchor is tuning-only.
