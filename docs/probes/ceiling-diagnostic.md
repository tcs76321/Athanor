# Cognitive-loop diagnostic (not the frozen benchmark)

**Status:** protocol · **Corpus:** [`eval/diagnostic/tasks.yaml`](../../eval/diagnostic/tasks.yaml)
**Purpose:** make the newer loop mechanisms observable on work that needs
iteration. It is **not** a benchmark version and must not be reported as a
headline score.

## Why a separate corpus

The frozen v2 corpus is the controlled study. It is mostly deterministic H/X
tasks; that is the right floor, but it does not require the notebook, a
reframe, a model-requested observation, or cross-job memory. Editing v2 would
break the freeze ([ADR-0075](../adr/0075-frozen-corpus-compass.md)), so the
diagnostic lives beside it.

The diagnostic is a **sensitivity instrument**, not a release gate. It answers:
when the loop's distinctive mechanisms matter, do they run, and do they help?

## Tasks

| Task | Capability | What it stresses |
|---|---|---|
| `diag-layered-code` | code | multi-condition validation, a shared failure path, self-refine and optional file observation |
| `diag-structured-brief` | document | exact headings/order/word limit, declared checks, notebook-carried limits |
| `diag-contradiction-brief` | document | contradictory source handling, required sections, no invented source |
| `diag-sequence-memory` | document | **three dependent goals in one project**; later steps must preserve the option chosen in step 1; `progress` and per-step checkpoints are scored |

## Run

```bash
make diagnostic
```

or directly:

```bash
make soak-mini \
  MINI_SOAK_CORPUS=eval/diagnostic/tasks.yaml \
  MINI_SOAK_ONLY=diag-layered-code,diag-structured-brief,diag-contradiction-brief \
  MINI_SOAK_OUT=tools/bench/results/diagnostic
```

The runner writes the ordinary `results.json` / `metrics.jsonl`. The new
telemetry counters are the point:

- `learned_selections` — the UCB candidate-count selector changed a plan.
- `replans` — a regulator reframe wrote a new plan.
- `observations` — a code repair requested and read a repository file.
- `clarifications` — the model asked a blocking question.
- `memory_remembered` — a completed job wrote a project memo.
- `memory_hits` — a later job retrieved memory (requires a multi-job project;
  the task corpus creates one project per task, so use the memory runner for
  cross-job evidence).

## Ablations (the actual study)

Run the same corpus three times and compare the mechanism counters and pass
rate:

| Arm | Config | Question |
|---|---|---|
| baseline | `observation_tools: false`, `pairwise_judge: false`, `learned_selection: false` | what does the fixed loop do? |
| mechanisms | defaults | do observation, learned selection, and reframe improve anything? |
| pairwise | mechanisms + `pairwise_judge: true` | does position-bias control change the open-ended rows? |

Report the **counter deltas** before the pass-rate delta. A mechanism that never
runs cannot have caused a change; a mechanism that runs but never changes an
outcome is a cost, not evidence.

## Sequence scoring

A task may declare `steps:` instead of one `goal`:

```yaml
- id: diag-sequence-memory
  steps:
    - goal: ...
      checks: {required_sections: [Decision], max_words: 120}
    - goal: ...
      checks: {required_sections: [Implementation], max_words: 160}
    - goal: ...
      checks: {required_sections: [Risks], min_items: [{noun: risks, min: 2}]}
```

The runner creates one project, submits each step as a follow-up goal, patches
the execution config for that step's test/checks, and records:

- `sequence[]` — per-step state, artifact, pass/fail, check failures;
- `progress` — passed checkpoints / total steps;
- the final step's deterministic outcome as the row's pass/fail.

The sequence stops at the first failed checkpoint. A three-step task that
passes step 1 and fails step 2 reports `progress = 0.33`, which is useful
even though the final pass is false.

Run the paired control with:

```bash
make diagnostic-pair
```

It writes `full/` and `single/` under `DIAGNOSTIC_OUT` and merges the report,
so the delta is per task rather than an aggregate.

## First run — 2026-11-06

Host path, `ornith9b`, full arm, one pass: **1/3** (`diag-structured-brief`
passed; the code and contradiction tasks failed).

The failures were not capability failures. Both parked on a clarification
question emitted during **reflection**, and with no operator present the job
timed out. The questions were meta-questions about the harness ("does the
verifier import `solution`?" and "does the section-order parser scan the
artifact?"), not missing task facts. That is a real design bug the diagnostic
caught:

- Clarification is now **opt-in** (`allow_clarifications: false` by default),
  so an unattended run cannot park itself.
- It is **planning-only**; reflection is expected to act, not ask the harness.
- The prompt forbids asking about the verifier, tools, or scoring.
- The duplicate `memory_remembered` audit for one job was fixed.

The rerun is the evidence for the fix. This is exactly the class of failure
the old v2 corpus could not surface.

## Second run — after the clarification fix

**2/3.** `diag-contradiction-brief` now passes; the code task still failed.
The clarification counter is zero for the contradiction task, and no job
parked. That is the intended behavior.

The code failure was a **language-detection bug**, not a model failure:
`DetectLanguage` did not recognize `python -c ...`, so the candidate took the
multi-file staging path instead of the single-file `solution.py` path. The
test command imported `solution`, found nothing, and failed every candidate
with `ModuleNotFoundError`. Fixed by recognizing plain `python` commands; the
code-task rerun is the evidence.

### Code task rerun

**1/1.** `diag-layered-code` now passes on the final language fix. Importantly,
it passed on the **first attempt**: the observation, replan, and repair
counters were zero. That is a saturation signal, not evidence for the loop.
The task is currently one-shot-solvable for this model; v3 needs an injected
failure, a hidden check, or a multi-step sequence to force the mechanisms to
run. This is exactly the distinction the v3 plan makes between ranking and
differentiating tasks.

## Final three-task run

**3/3** on the final binary (`diag-layered-code`, `diag-structured-brief`,
`diag-contradiction-brief`). The per-job telemetry shows no observations, no
replans, no repair loops, and no reflection loops: all three were first-pass
successes. The report now says:

> **Saturated:** 3/3 completed rows were first-pass successes (no repair,
> reflection, replan, or observation). They are ranking rows, not evidence
> for the iterative loop (ADR-0116).

Raw evidence: [`runs/bench-diagnostic-20261106/`](runs/bench-diagnostic-20261106/).

This is the correct result for the current diagnostic: it validates the
harness after the two fixes, and it demonstrates that these tasks are too easy
to test Athanor's distinctive machinery. The next diagnostic version needs an
injected failure, a hidden check, or a multi-step sequence.

## First sequence run — 2026-11-06

`diag-sequence-memory` is the first differentiating task: step 1 passed, step 2
failed, step 3 never ran. **Progress 1/3.**

The mechanism counters are the point:

- **23 memory hits** — the step-2 job retrieved prior project memory.
- **2 repair loops, 3 reflection loops, 1 replan** — the loop tried to
  recover.
- It still failed the step-2 checks: every candidate was missing the required
  `Implementation` heading (`missing required section: Implementation`). That
  is a real contract/capability failure, not a harness failure.

This is qualitatively different from the one-shot tasks: memory was actually
used, the iterative machinery ran, and the step still failed. That is a real
capability signal, not a harness failure (the earlier harness failures were
clarification parking and `python -c` staging).

The first run also exposed a reporting bug: the sequence row inherited
step 1's `check_pass=true` while `state=failed`, producing `failed / pass=true`.
The runner now resets the checkpoint state per step. A second run exposed a
scoring bug: step 2’s **final artifact** had the required `Implementation`
heading, but the checkpoint inspected the best intermediate candidate, which
did not. A sequence step is now judged on its delivered artifact first, with
the best candidate only as a fallback.

### Corrected run — final

**Progress 1/3, final pass false.** Step 1 passed. Step 2 failed after
**1 repair loop, 2 reflection loops, 1 replan, and 23 memory hits**. The
failure was the required `Implementation` heading. Step 3 never ran. The
sequence row now reports `state=failed, pass=false`, and the report shows
33% progress with two steps executed.

Raw evidence:
[`runs/bench-diagnostic-sequence-20261106/`](runs/bench-diagnostic-sequence-20261106/).

### Paired control — corrected

| Arm | Passes | Progress | Step-2 failure |
|---|---:|---|---|
| `full` | 0/3 | 1/3 each | required `Implementation` heading |
| `single` | 1/3 | 3/3 once; 1/3 twice | required `Implementation` heading |

The first single-arm run passed all three checkpoints; two repeats failed at
the same step-2 heading check. **This is not a negative delta and not evidence
for the loop.** It is a single-run false signal: the task is hard for both arms,
and one pass is not enough to compare them. At n=3 per arm the honest statement
is "no measured difference; both mostly fail at the same checkpoint."

Raw single-arm evidence:
[`runs/bench-diagnostic-sequence-single-20261106/`](runs/bench-diagnostic-sequence-single-20261106/).

The next study should run more passes and the `bestof3` / `reflect` / `full`
ablation, and inspect the candidate sets on the failing step. The diagnostic
has already taught the most important lesson: **a single paired pass can
produce a completely wrong conclusion.**

## Caveats

- One model and one pass are directional only. This is a diagnostic, not a
  statistical result.
- The tasks are small on purpose: they are probes for the loop, not a claim
  about general capability.
- The frozen v2 corpus remains the per-major controlled study. This diagnostic
  must never be used to re-label a v2 result or to tune the held-out slice.
