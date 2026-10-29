# X-code focus after the P0 working-set pack

**Status:** diagnostic run · **Date:** 2026-10-29 · **Models:** `ornith9b` (generator), plus a `bonsai9b` quantization screen / `granite4.2:3b` (judge) · **Corpus:** v2 X-code slice, one pass
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
must be a correct importable module.

### Final Core Pod rerun

With the scratch mount and module-name fixes, the clean Core run produced
**3/6** (50%):

| Task | Host diagnostic | Core Pod final |
|---|---|---|
| `x-code-repo-ratelimit` | ✅ | ✅ |
| `x-code-bugfix-pipeline` | ✅ | ✅ |
| `x-code-interpreter` | ✅ | ❌ real parser `NameError` |
| `x-code-diff3` | ✅ | ✅ |
| `x-code-threadpool` | ❌ | ❌ real threadpool test failure (no longer a hang) |
| `x-code-sqlite-migration` | ❌ | ❌ real checksum/rollback logic failure (the import now succeeds) |

Raw evidence: [`runs/bench-xcode-core-20261029/`](runs/bench-xcode-core-20261029/)
(report, results, config, metrics).

The import class is closed: sqlite-migration now reaches its assertions and
fails on checksum/rollback semantics; threadpool fails its test instead of
hanging. The Core Pod run is 3/6 versus the host diagnostic's 4/6; one pass and
one model, so this is directional, not a runtime regression claim. It also
confirms the remaining failures are candidate logic, not missing repository
context.

## Quantization diagnostic: `bonsai-2:9b` (2026-10-29)

A second model was screened on the same six tasks and the same host runtime to
separate model quality from the harness fixes. `bonsai-2:9b` is Qwen3.5 9B at
**Q2_0** with a 262k native window; the probe pins `context_target: 32768` and
`think: false`, so the window is not exercised. It is a diagnostics-only label,
outside the frozen v2 roster.

| Task | `ornith9b` (Q4_K_M) | `bonsai9b` (Q2_0) |
|---|---|---|
| `x-code-repo-ratelimit` | ✅ 62.8 s / 18.4k tok | ❌ 147.8 s / 51.5k tok |
| `x-code-bugfix-pipeline` | ✅ 53.5 s / 15.9k tok | ✅ 49.8 s / 14.1k tok |
| `x-code-interpreter` | ✅ 164.1 s / 20.7k tok | ❌ 888.6 s / 110.8k tok |
| `x-code-diff3` | ✅ 155.7 s / 27.6k tok | ✅ 132.3 s / 18.3k tok |
| `x-code-threadpool` | ❌ 1126.2 s / 40.9k tok | ❌ 394.1 s / 59.5k tok |
| `x-code-sqlite-migration` | ❌ 346.2 s / 56.9k tok | ❌ timed out at 20 m |

Result: **2/6 (33%)** versus `ornith9b`'s 4/6 on the same host run.

On the two tasks both models solve, bonsai is ~9% faster (190.9 s vs 209.2 s) and
uses ~25% fewer tokens (32.5k vs 43.5k); on `bugfix-pipeline`, which ran the same
eight calls, completion throughput was ~20% higher. That is the expected Q2
advantage. It is swamped by the failure paths: `interpreter` took 5.4× as long
and still failed, and `sqlite-migration` consumed the full 20-minute job budget
without producing a scoreable artifact.

The honest summary is **quicker when it is right, slower when it is wrong**. A
fast wrong answer pays the whole retry budget, so tokens/second is not the
metric — accepted outcomes per unit of budget is. This run is model evidence, not
a working-set or Core Pod claim, and it does not change the frozen v2 result.

Raw evidence: [`runs/bench-bonsai-xcode-20261029/`](runs/bench-bonsai-xcode-20261029/).

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
