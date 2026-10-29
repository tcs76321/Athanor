# X-code focus after the P0 working-set pack

**Status:** diagnostic run · **Date:** 2026-10-29 · **Model:** `ornith9b` (generator) / `granite4.2:3b` (judge) · **Corpus:** v2 X-code slice, one pass
**Raw evidence:** [`runs/bench-xcode-p0-20261029/`](runs/bench-xcode-p0-20261029/) (report, results, config, metrics)

This is a focused diagnostic, not the frozen headline run: six X-code tasks, one
model, one pass, on the **host development runtime**. The frozen v2 result
remains the 2026-10-25 Core Pod run ([bench-v2-findings](bench-v2-findings.md));
this run asks whether the working-set pack moves the X-code wall before we spend
time on a read-only tool loop.

## Run shape

| Field | Value |
|---|---|
| Tasks | `x-code-repo-ratelimit`, `x-code-bugfix-pipeline`, `x-code-interpreter`, `x-code-diff3`, `x-code-threadpool`, `x-code-sqlite-migration` |
| Arm | `full` (N=3, reflection, self-refine) |
| Passes | 1 |
| Runtime | host daemon, `bin/athanor` from the P0 pack/memory/cancel tree |
| Wall clock | 33 m 25 s |
| Job rows | 6 |
| Working-set mode | `auto` (code only) |
| Memory | empty store in this run; 0 memory hits |

## Headline

| Slice | Pass | Rate |
|---|---:|---:|
| X-code, all six | **4/6** | **67%** |
| X-code in the 2026-10-25 full v2 run | 0/6 | 0% |
| Held-out among these six | 2/2 | 100% |
| Tuning among these six | 2/4 | 50% |

Passing: `x-code-repo-ratelimit`, `x-code-bugfix-pipeline`,
`x-code-interpreter`, `x-code-diff3`.

Failing: `x-code-threadpool`, `x-code-sqlite-migration`.

This is directional: six tasks, one model, one pass, and the held-out pair is
too small to generalize from. Do not treat it as a benchmark-version result.

## Mechanism telemetry

From the run's report:

| Metric | Value |
|---|---:|
| Working-set chunks injected | 45 |
| Working-set bytes injected | 12,435 |
| Dormant Index rows | 88 |
| Memory hits | 0 |
| Compactions | 0 |
| KV warnings / critical | 0 / 0 |
| Context-floor violations | 0 |
| Repairs (self-refine) | 3 |
| Reflection loops | 5 |
| Mean candidate diversity | 0.650 |
| Code accepts decided without the judge | 4/4 |

The pack was active in every code job; the prompt did not breach a floor.

## Why the two failures are not a missing-context story

**`x-code-threadpool`** repeatedly failed the real test and hit
`tool_deadline_exceeded` on `run_tests` for several candidates. The job spent
~18.8 minutes and ended `failed` after reflection. This is a candidate that
deadlocked/hung the test, not a candidate that lacked repository bytes. The tool
deadline contained it (the daemon stayed healthy).

**`x-code-sqlite-migration`** failed every candidate on the test's import:

```text
test command failed: python -c 'import ... from migrator import Migration, apply, rollback, MigrationError ...'
```

The reflection identifies the mechanical issue: the test requires the artifact
to be importable as **`migrator.py`**, but the task text does not state that
filename, and candidates produced a differently named or incomplete module. That
is a task-contract/harness fairness issue first, a capability issue second. The
model had the repository working set; it did not have the required module name.

## Core Pod rerun (2026-10-29) and the module-name defect

The first Core Pod rerun produced **2/6** (repo-ratelimit and
bugfix-pipeline passed). Interpreter and diff3 were scored 1.0 by the engine
but ended `failed`; their event traces show a consistent
`ModuleNotFoundError: No module named 'exprlang' / 'merge3'`. The engine
materialized every single-file candidate as `/tmp/work/solution.py`, while the
test command imported a named module. The test command was visible to the model
after the fairness fix, but the harness still wrote the wrong filename.

A follow-up engine fix infers the module name from the test command
(`from exprlang import …` → `exprlang.py`) and stages the single-file candidate
under that name. This is still a harness fix, not a corpus edit: the candidate
must be a correct importable module. A final Core Pod rerun is required before
comparing the runtime to the host result; the 2/6 run is not a Core-runtime
regression claim.

Threadpool and sqlite-migration continued to fail: the former on a candidate
test hang, the latter on the same import/module-name class. The module-name fix
targets the latter; threadpool remains a candidate-deadlock/test-hang finding.

## Decision

1. **Do not implement the bounded read-only recon loop yet.** The pack moved the
   slice from 0/6 to 4/6, and neither remaining failure is explained by missing
   file content. Revisit the loop only if a corrected task contract still fails
   with a context-shaped failure.
2. **Fix the sqlite-migration task contract** (state the required `migrator.py`
   path, or make the test discover the artifact) before using it as capability
   evidence. This is a benchmark-definition issue; the frozen v2 corpus is not
   edited retroactively — cut the next version or record the task as a known
   confound.
3. **Audit the threadpool test for a hang** and decide whether the timeout is a
   candidate failure or a test-design failure. The tool deadline did its job.
4. **Re-run on the Core Pod** before treating the delta as release evidence; the
   host runtime is a development tool (ADR-0078).

## Caveats

- One model, one pass, n=6; no statistical claim.
- Two of the six are held-out; the 2/2 held-out result is too small to interpret
  as generalization.
- The run used the host runtime, not the Core Pod.
- The frozen v2 corpus and its digest are unchanged.
