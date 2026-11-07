# ADR 0118 — One-shot first, escalate on failure

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §13.1; ADR-0074 (attempt escalation), ADR-0110 (learned
selection), ADR-0116 (benchmark portfolio), ADR-0117 (quality monotonicity);
`internal/engine`, `tools/bench`

## Context

The full loop always generates N candidates up front. For a task that one
shot solves, that is three generations and three evaluations for no quality
gain. The benchmark's `single` arm exists, but the engine does not use
"did one shot work?" as a gate; it pays for the full panel regardless.

The user-facing principle is simple: **first try one shot; if it passes, stop;
escalate only when it fails.** The full loop is the fallback, not the default
tax.

## Decision

Add an opt-in **one-shot-first** policy:

- `execution.policy.one_shot_first` (default false).
- On a job's **first divergence cycle**, if the policy is on and the candidate
  count is >1, generate **one** candidate. Audit `one_shot_first`.
- If that candidate passes, the normal state machine proceeds to synthesis
  and comparison; no extra candidates are generated.
- If it fails, the reflection/regulator cycle runs; the next divergence cycle
  has an evaluation record, so `oneShotFirst` returns false and the full
  candidate count is used. The escalation is automatic.
- The benchmark gains a `cascade` arm (Candidates 3, one_shot_first true) and
  `make diagnostic-cascade`. `full` remains the unconditional control; `single`
  remains the one-shot baseline.

This is a **cost** optimization, not a quality trade: the full candidate set
is still available after a failure. The quality-monotonicity rule (ADR-0117)
guarantees the later full attempt cannot deliver a worse artifact than the
best candidate already seen.

## Consequences

- Easy tasks cost one generation + one evaluation instead of N.
- Hard tasks pay one extra small step (the first candidate) before the full
  panel; that is the price of the gate.
- The benchmark can report three points: single, cascade, full. A cascade that
  matches full at lower cost is the desired operating shape; a cascade that
  matches single at lower cost means the task was saturated.
- The default is false to keep the `full` benchmark arm a stable control.
  Production can enable it; a later default flip should be an explicit ADR
  after the cascade study.

## Alternatives rejected

- **Always full.** It taxes every easy task for a failure that did not happen.
- **Always single.** It loses the recovery and diversity the full loop provides
  when the first attempt fails.
- **Pick based on difficulty alone.** The planner's difficulty hint is weak
  (M9-T4); the first attempt's actual result is the strongest signal.
