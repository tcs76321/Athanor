# Evidence pipeline — design

**Status:** proposed (implementation pending) · **Date:** 2026-10-17 · **Refs:**
[ADR-0076](adr/0076-continuous-evidence.md) (continuous evidence),
[ADR-0075](adr/0075-frozen-corpus-compass.md) (frozen-corpus soak),
[ADR-0073](adr/0073-soak-validation-harness.md) (soak harness).

The keystone of ADR-0076: **real use and soaks must share one evidence path.**
Today the reducer (`tools/bench/telemetry.go`) and the report/leaderboard
read a probe results tree, not a daemon state dir. This note specifies the
smallest surface that makes any state dir readable as evidence.

## 1. One reducer, two inputs

```
soak results tree ─┐
                   ├─► jobMetrics[] ─► telemetry ─► report.md / report.json / measurements.json
daemon state dir ──┘
```

The soak's `jobMetrics` rows come from the probe's matrix (goal, model, arm,
run, checks). A state-dir row has no arm or probe check, so it is populated
conservatively:

| field | soak | state dir |
|---|---|---|
| GoalName | corpus task name | task title (bounded) |
| Archetype | corpus archetype | project archetype |
| ModelLabel / Model | matrix label/tag | the most frequent `model` across the job's `llm_call` events |
| Arm | arm name | `"real"` |
| Run | pass number | 1 (or a session index) |
| Tier | E/M/H | `"IRL"` |
| Winner/Score/Confidence/TokenCost/WallMS/Retries/ReflectionLoops | `strategy_outcomes` | `strategy_outcomes` |
| Diversity/Candidates | divergence event / artifacts | same helpers |
| Telemetry | event reducer | same reducer |
| CheckPass | probe-side checks | `nil` (no probe checks; `jobPassed` falls back to accept) |

Lives in the probe first (`m3-t7-probe evidence -state-dir <dir> [-out <dir>]`),
reusing `openResultsDB`, `fillCandidateMetrics`, `fillVerification`,
`fillTelemetry`, `renderReport`, and `renderLeaderboard`. The daemon may later
expose the same reduction as `athanor report`; the reducer is the stable part.

## 2. Sessions and provenance

A **session** is one daemon lifetime or an explicit operator boundary. Every row
carries: job → project → task → artifact → model/config → version (`athanor
version`) → timestamps. Without this join, a label cannot be attributed.

## 3. Labels (derived, never typed)

1. **Deterministic** — tests/checks/schema (`jobPassed` already prefers them).
2. **Downstream reality** — did the accepted artifact get used? Signals:
   git-as-undo commit, export, push, a later edit of the same file, a re-run of
   the task, abandonment. These are joins over existing tables/artifacts.
3. **Implicit operator actions** — accept/reject, retry, cancel, correction,
   time-to-first-action.
4. **Periodic human eval** — the anchor (below).
5. **LLM judge** — advisory only; never a label.

## 4. Human eval review queue

A sampled, **blinded** set (artifact/diff/cost, no model name) with a short
rubric; ratings are stored with provenance and joined back to the run. This is
the "daily eval" signal and the calibration anchor for the judge. Sampling
should over-weight: new task classes, failed-but-confident verdicts, and
accepted artifacts that were later edited/abandoned.

## 5. Retention and the held-out rule

- Keep **raw** artifacts/events/verdicts per version so a future grader can
  re-score (ADR-0075 §4); compact only what is not decision-relevant.
- **Complete capture is not a clean training set**: dedup, contamination-check,
  and never tune on the frozen corpus's held-out slice (ADR-0075 §3).

## 6. Anti-self-grading invariant

A reported improvement must show **both**: real-work movement (fewer retries,
lower tokens per accepted artifact, fewer corrections) **and** non-regression on
the frozen held-out corpus. Neither alone is a claim.

## 6a. Experiment records and the approval ledger

A night of work ([ADR-0077](adr/0077-two-day-cycle.md)) produces **experiment
records**, not just prose:

- **hypothesis** → **spec** (config variation, task set, budget) → **samples** →
  **result** → **proposal**;
- shadow/draft only, budgeted, preemptible, never on the held-out corpus;
- stored with provenance (project, conversation, config, version).

The persistent **digest is the approval ledger**: each item records the
hypothesis, the experiment, its samples/effect, the operator's decision
(approved / deferred / rejected), and provenance. Confirmed **project-scoped**
adjustments become policy; **global** or **untested** proposals stay queued
(two-axis rule).

## 7. Implementation steps

1. `evidence` subcommand + state-dir reducer over `jobs`/`strategy_outcomes`/
   `events`; tests against a fixture DB. Read the state from the **Core Pod's
   mounted volume** (the only supported runtime, ADR-0078).
2. Downstream-use joins (git commit, export/push, re-run, correction) as label
   columns.
3. Review-queue export (sampled, blinded) + ratings store.
4. **Experiment records + shadow runner** (bounded; never held-out).
5. **Persistent digest = approval ledger** with the two-axis decision.
6. Surface the same reducer in the daemon/UI once the first-use surface is
   settled ([first-use.md](first-use.md)).

## 8. Open questions for the UX workflow

- Where does the operator see "what it did and cost" — the UI, `athanor report`,
  or both?
- What is the session boundary (daemon lifetime, calendar day, explicit)?
- Does the review queue live in the UI or as an exported file?
