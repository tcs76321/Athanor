# Frozen benchmark v3 — plan (cut at 1.0.0)

**Status:** planning · **Refs:** [ADR-0116](adr/0116-benchmark-portfolio.md),
[ADR-0075](adr/0075-frozen-corpus-compass.md),
[ADR-0092](adr/0092-benchmark-label-integrity.md),
[ceiling diagnostic](probes/ceiling-diagnostic.md)

v2 remains the 0.0.0 instrument. v3 is **not** an edit: it is a new corpus cut
at 1.0.0, designed around what Athanor claims to add over a one-shot model.

## The headline

Report a **paired delta**, not a bare pass rate:

```text
saturation_adjusted_delta =
  mean over differentiating tasks of (pass_athanor - pass_single)
```

Report the ranking slice separately:

```text
saturated = tasks where pass_single == pass_athanor == true
```

A high absolute pass rate is not a claim; a positive delta on
differentiating tasks is. A negative delta on any family is a release blocker.

## Task families and examples

### A. One-shot ranking (20%)

Real deliverables that a strong model can solve in one shot. Athanor's job is
to rank N candidates/model variants and pick the best, not to take many steps.

- A precise data transform with a JSON Schema.
- A short policy brief with declared checks.
- A small single-file utility with a test command.
- A constrained rewrite (tone, length, forbidden phrases).

**Score:** external check; report candidate count, selection reason, cost, and
whether the winner came from a different model/family.

### B. Iterative repair (25%)

The first attempt should be genuinely likely to fail; the task is to observe,
diagnose, and repair.

- Three failing tests in a provided package; fix all without breaking the rest.
- A migration that fails on an edge row; diagnose and repair.
- A flaky test; find the race and make it deterministic.
- A dependency/version mismatch; repair the build and keep the public API.
- A generated module with a hidden test that exercises an unstated edge.

**Score:** final external tests + final diff + number of repair/observation
cycles. A pass after one observation is better evidence than a blind retry.

### C. Long-horizon project (25%)

A project with 3–6 dependent goals over separate jobs. Memory and the notebook
must carry decisions across jobs; the graph must survive a failure.

- Build a CLI, then add a subcommand, then fix a regression.
- Build a schema, migrate a fixture, validate the result, document it.
- Write a plan, execute step 1, revise the plan after a failed checkpoint, finish.
- Add an endpoint, tests, a migration, and a README; one goal fails and must
  be recovered or replanned.

**Score:** checkpoint progress + final external pass + notebook/memory
utilization + replan count. A final pass with no memory use is a weaker result
than one that retrieved the earlier constraint.

### D. Research and ambiguity (15%)

- Three sources disagree; produce a cited brief that names the conflict and
  chooses conservatively without inventing a source.
- Summarize a long document under a citation contract.
- Explain a bug from logs and propose a falsifiable fix.

**Score:** deterministic source/citation checks + human-anchor rubric. The LLM
judge is advisory only.

### E. Operational autonomy (10%)

Scenario tests, not generation quality:

- Kill the Core mid-diverging, restart, assert resume and no lost state.
- Freeze during a job; assert no new work and a reason-gated unfreeze.
- File a clarification; assert the job parks and the answer precedes resume.
- Exhaust a budget; assert defer, not failure; assert resume in the next window.
- Crash the pod; assert sweep and a clean retry.

**Score:** scenario assertions + state checks. No model required.

### F. Safety and containment (5%)

Reuse the existing adversarial corpora: injection, exfiltration, unsafe
request, SSRF, allowlist bypass. **Score:** fail-closed and audited.

## Harness work required

1. **Sequence tasks.** A task can declare `steps:` in one project, with
   per-step checks and a final measure. The memory runner already has this
   shape; generalize it to arbitrary task steps (not only corrections/insights).
2. **Checkpoint scoring.** The runner records per-step pass/fail and a
   `progress` fraction. The report shows progress and final pass separately.
3. **Single-shot control.** Every deterministic task runs `arm=single` and
   `arm=full` on the same corpus/model. The report pairs them by task id.
4. **Adversity hooks.** Crash/restart, freeze, budget stop, missing model, and
   HITL scenarios as first-class steps, not post-hoc scripts.
5. **External scoring.** Frozen checks and tests remain the headline; the
   engine's verifier/judge never labels its own success.
6. **Process metrics.** Extend the telemetry reducer with the counters already
   added for the diagnostic (learned selection, replan, observation,
   clarification, memory write/read).

## Cut criteria

At the v3 cut:

- The corpus digest is frozen; `make corpus-check-v3` passes.
- Every deterministic task has a paired single-shot baseline.
- Every family has at least one held-out task.
- The report shows the saturation-adjusted delta and the mechanism counters.
- A no-op change (same model, same config) reproduces the previous delta within
  the documented variance.
- v2 remains runnable and reported as the historical floor.

## What not to do

- Do not edit v2 to add multi-step tasks. Cut v3.
- Do not count a task where single-shot passes as evidence for iteration.
- Do not tune on the held-out slice.
- Do not use the engine's own judge as the headline.
