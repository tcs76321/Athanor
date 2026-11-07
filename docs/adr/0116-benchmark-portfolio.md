# ADR 0116 — A benchmark portfolio, not a one-shot leaderboard

**Status:** Accepted for v3 planning · **Date:** 2026-11-06 · **Refs:**
ADR-0061 (harder benchmark), ADR-0075 (frozen corpus), ADR-0076
(continuous evidence), ADR-0092 (label integrity); `eval/bench/v2`,
`eval/diagnostic`, `tools/bench`

## Context

The frozen v2 corpus is a strong instrument, but most of its tasks are
**one artifact from one goal**: implement `solution.py`, write a brief, produce
a JSON array. The engine has multi-phase machinery (planning, divergence,
evaluation, synthesis, comparison), and the full arm runs N candidates, but the
task itself does not require iteration, tool observation, cross-job memory, a
human question, or recovery. A strong model can pass many rows single-shot.
The F4 closeout already found no consistent advantage for the loop on bounded
code.

That means the v2 headline is mostly a **model-capability** score with an
Athanor wrapper. It cannot answer the question that matters: does Athanor's
iteration and infrastructure beat a one-shot model on work that actually needs
it, while staying reliable and useful?

The memory runner and the new `eval/diagnostic` corpus are steps toward
multi-step work, but they are not a portfolio: the memory runner is partial and
the diagnostic is three one-job probes. A v3 benchmark must be designed around
the capabilities Athanor claims.

## Decision

Cut benchmark **v3 at 1.0.0** as a portfolio with an explicit one-shot control
and process scoring. Do not edit v2.

### Task families

| Family | Share | What it must require | Scoring |
|---|---:|---|---|
| **One-shot ranking** | 20% | A single model can plausibly solve it; Athanor should win by selecting among N candidates/models | external pass + paired single-shot baseline |
| **Iterative repair** | 25% | A failing test/diagnostic, a multi-file change, or a hidden check; one attempt is unlikely | final external pass + repair steps + final diff |
| **Long-horizon project** | 25% | 3–6 dependent goals in one project; decisions/constraints must carry across jobs | checkpoint progress + final pass + memory/notebook use |
| **Research & ambiguity** | 15% | Contradictory sources, citation integrity, plan quality; no single right string | deterministic citation/source checks + human anchor |
| **Operational autonomy** | 10% | Crash/restart, freeze/unfreeze, HITL question/approval, budget deferral, recovery | scenario assertions + no state loss |
| **Safety & containment** | 5% | Injection, exfiltration, unsafe request | existing adversarial corpora |

A task is **Athanor-differentiating** only if it requires at least one of:
multiple candidates, an observation/repair cycle, cross-job memory, HITL, or
recovery. A one-shot-solvable task stays in the ranking slice and is reported
as **saturated**, not used as evidence that the loop works.

### Required controls

1. **Paired single-shot baseline.** Every deterministic task runs a `single`
   arm with the same model/persona set. Report `pass_single`, `pass_athanor`,
   and the delta. A task where both pass is saturated; a task where both fail
   is out of reach, not evidence.
2. **Process metrics.** Report notebook entries used, replans, observation
   reads, clarifications, memory hits/writes, candidate diversity, reflection
   loops, retries, HITL interventions, tokens, and wall time. The existing
   telemetry reducer is the base; the diagnostic counters are the start.
3. **Checkpoints.** Long-horizon tasks expose 2–5 deterministic checkpoints
   (e.g., migration applied, endpoint responds, tests pass, docs exist). Score
   progress separately from final acceptance.
4. **Injected adversity.** At least one recovery scenario per run: a failing
   test, a crash/restart, a missing model, a budget stop. Score recovery and
   state preservation, not only the final artifact.
5. **External scoring.** The headline uses frozen external checks, never the
   engine's verifier or judge (ADR-0092).
6. **Held-out families.** Freeze task families, not just strings; no task-level
   tuning.

### What this means for v2

v2 remains the 0.0.0 historical instrument and the regression floor. It is not
re-cut. Its results are reported with an explicit **one-shot-solvable** caveat
where applicable, and the diagnostic is the bridge: it measures mechanism use
now, while v3 measures the difference on work that needs the mechanisms.

## Consequences

- The headline becomes a **delta over one-shot**, not an absolute pass rate.
- A task that a basic model solves one-shot is not deleted; it is the ranking
  slice and a saturation check.
- The benchmark finally measures the infrastructure claims (pods, memory,
  recovery, HITL) as well as model quality.
- v3 is more expensive to build: sequences, checkpoints, and injected adversity
  are harness work, not just YAML. That work starts with the diagnostic runner
  and the memory-sequence runner, not with a new frozen tag.

## Alternatives rejected

- **Keep adding hard one-shot tasks to v2.** It raises model difficulty without
  testing iteration, memory, or recovery. It also cannot separate model gains
  from loop gains.
- **Use the engine's own acceptance as the score.** It is not independent
  (ADR-0092).
- **Only report pass rate.** It hides cost, interventions, and whether the
  mechanisms ran.
