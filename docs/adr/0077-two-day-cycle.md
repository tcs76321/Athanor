# ADR 0077 — The two-day human-eval cycle: work → dream → shadow experiments → confirm

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
[ADR-0075](0075-frozen-corpus-compass.md) (frozen-corpus soak),
[ADR-0076](0076-continuous-evidence.md) (continuous evidence),
[ADR-0064](0064-cognitive-operations.md) (operations),
[ADR-0067](0067-technique-guidance-loop.md) (technique guidance); AGENTS
"repertoire vs. selection".

## Context

Real use is the distribution that matters, but the operator is the scarce
independent label. A purely *passive* overnight analysis on day-1 evidence
cannot justify changing routing or technique selection — one project is a
sample, and the M9 soak showed a single pass is noise (6/6 → 0/2). Waiting for
weeks of passive data before the first adjustment is too slow for the product,
and acting on an untested hypothesis is self-grading.

The answer is an **active** night: the day's human evals seed hypotheses, and
the night **tests them in shadow** so the next morning confirms evidence, not a
hunch.

## Decision

**1. The cycle is two days.**
- **Day 1** — work plus human evals (plan approval, steering, any review).
- **Night 1** — the dreaming pass analyses the human-eval input, forms
  **hypotheses**, and runs **bounded shadow experiments** to test them.
- **Day 2** — review the work *and* confirm the conclusions, which now carry
  samples and an observed effect.
- **Night 2+** — digest the confirmation, form the next hypotheses, test them.

**2. An experiment is a first-class object.** `hypothesis → spec → samples →
result → proposal`. A spec names the config variation, the task set, and the
budget. Experiments are **shadow/draft only** (never accept, commit, push, or
mutate the accepted project state), **budgeted**, **preemptible**, and **never
run on the frozen corpus's held-out slice** (that is the referee, ADR-0075).

**3. Two-axis approval.** Evidence × scope decides what day 2 may approve:

| | Project-scoped | Global policy (routing, technique activation, thresholds) |
|---|---|---|
| **Tested overnight (bounded, shadow)** | **Confirmable day 2** — cite samples + effect | Propose; still needs cross-project or held-out evidence |
| **Untested hypothesis** | Propose only | Propose only |

A tested, project-scoped adjustment may be confirmed on day 2; a global change
graduates only after more than one project (or the frozen corpus) supports it.

**4. The night is HITL-free by construction.** Anything external, irreversible,
or requiring a human decision is **queued for the morning**, never attempted
overnight. This is what makes "step away" safe.

**5. The digest is the approval ledger.** It persists each item: hypothesis,
experiment, samples, result, decision (approved / deferred / rejected), and
provenance (job/project/conversation). Without it, the next night cannot
attribute outcomes and the loop regresses to self-grading.

**6. Confirmed conclusions become policy; rejected ones become negative
evidence.** The next night starts from both.

## Consequences

- A budget model is required: a nightly envelope (time/tokens) split across
  hypotheses, with preemption by the next interactive request.
- A shadow experiment runner and an experiment record are new deliverables.
- The digest must be persistent and interactive (a morning meeting), not the
  current computed summary.
- Day-2 approvals are reversible and recorded; model/technique changes are
  queued as experiments until their sample guard is met.
- The frozen-corpus soak remains the external referee ([ADR-0075](0075-frozen-corpus-compass.md));
  real use feeds selection ([ADR-0076](0076-continuous-evidence.md)).

## Alternatives rejected

- **Passive analysis only.** Correct but too slow: no adjustment for weeks.
- **Unfiltered overnight action.** Blocks on a human at 3am or takes external
  actions without approval.
- **Confirm untested, global changes on day 2.** Self-grading at the first
  opportunity; one project is not a policy.
- **A longer first cycle (three days).** Adds latency without adding evidence;
  the night can test what the morning needs.
