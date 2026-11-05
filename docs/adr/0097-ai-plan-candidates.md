# ADR 0097 — AI-authored plan candidates, human-selected

**Status:** Accepted — first implementation slice · **Date:** 2026-11-04 ·
**Refs:** ARCHITECTURE §13 (dialectical engine), §15 (brainstorming workflow),
§19 (evaluation); [ADR-0032](0032-dag-decomposition.md) (DAG validation),
[ADR-0088](0088-project-interview-plan-gate.md) (plan gate),
[ADR-0094](0094-project-discovery-portfolio.md) (discovery),
[ADR-0095](0095-first-class-gui-and-onboarding.md) (project onboarding).

## Context

Discovery gives the operator a portfolio of directions, but the next step was a
single deterministic 30-day plan derived from the chosen option. It is cheap
and predictable, yet it is one plan, written by a template rather than by the
model that just reasoned about the direction. The operator cannot compare a
fastest path, a highest-quality path, and a lowest-cost path before committing
machine time.

Reusing the dialectical engine is the wrong answer. Its divergence/evaluation/
comparison phases are designed to **keep one winner**; plan authoring needs the
opposite — three reviewable alternatives, preserved until a human chooses. The
model is useful as a proposer, not as the decider: it has no authority to start
work, accept artifacts, or bypass verification.

## Decision

1. **The model proposes exactly three plans.** One bounded inference call
   produces a JSON array of three genuinely different candidates: fastest
   validation, deepest quality, and lowest cost/least risky. Each candidate
   carries a title, summary, 4–8 tasks, risks, assumptions, and a confidence.
   Each task carries a title, description, one primary capability
   (`code`, `data`, `document`, `text`, `media`), and at least one acceptance
   criterion. The prompt forbids inventing market data and permits only the
   human-supplied sources as citations.
2. **The validator and scorer constrain the model.** The response is parsed,
   the array must contain three distinct titled candidates, task titles must be
   unique within a candidate, capabilities must be valid, and every task must
   have a criterion. The deterministic score (0–100) is computed from task
   coverage, criteria, risks, assumptions, description specificity, and
   confidence; confidence is clamped to 0–1 before scoring. The score is a
   reading aid, never an automatic selection and never a verdict.
3. **Sessions are durable and option-scoped.** Migration 0035 adds
   `plan_sessions` and `plan_candidates`. A session starts as `generating`,
   becomes `ready` with three candidates, or `failed` with an error that the UI
   can show. It records the discovery option it was generated for, so after a
   reset or a different direction an old ready plan is ignored rather than
   approved by accident. A generation still marked `generating` after 20
   minutes is reported as interrupted and can be retried; retrying starts a new
   session, preserving the old row for audit.
4. **The human chooses, then approves.** Selecting a candidate records
   `selected_index` and a `plan_selected` project event. Approval converts the
   chosen candidate into a dependency-ordered task graph (each task depends on
   the previous one, the smallest safe graph for an ordered plan) and starts it
   through the normal scheduler. No plan auto-starts, and the plan itself does
   not accept artifacts.
5. **The deterministic plan remains the fallback.** If planning is not
   configured, the model call fails, or the operator never selects a candidate,
   approval uses the existing deterministic 30-day plan. The AI path is an
   explicit button, not a hidden default, so it spends inference only when the
   operator asks.
6. **Normal execution rules still apply.** Plan tasks go through the same
   capability inference, verifier, Job Pod, and HITL gates as any other task.
   Planning is a Core-side inference call, not a Job Pod and not a tool call;
   it never fetches a source, so the gateway allowlist still governs every byte
   that is actually read.
7. **Audit the lifecycle.** `plan_started`, `plan_completed`, `plan_failed`,
   and `plan_selected` events name the option, session, candidate count, and
   error where applicable.

## Consequences

- The operator can compare three defensible paths before committing work, and
  the deterministic plan stays available as a zero-cost fallback.
- One plan generation costs one bounded model call. `inference.max_output_tokens`
  caps it; a truncated JSON response fails loudly and can be retried.
- Plan sessions accumulate as project history. Only the newest session for the
  project is shown; a plan is tied to the direction it was generated for.
- The selected plan is currently reviewable in onboarding only. Saving it as a
  plan artifact/tab on the project page is a follow-up, not part of this slice.
- The linear task graph is intentionally conservative; explicit task edges are
  future work, and the existing DAG validator remains the gate when they land.
- The benchmark corpus is unchanged. Planning is an onboarding surface, not a
  benchmark capability, so the frozen study is unaffected.

## Alternatives rejected

- **Auto-select the highest score.** The score measures structure, not the
  operator's constraints, risk tolerance, or taste; starting work on it would
  turn a reading aid into a decision.
- **A single free-form Markdown plan.** It cannot be validated, scored, or
  converted to tasks deterministically, and it hides whether the model followed
  the capability/criteria contract.
- **The dialectical engine.** Its comparison phase discards candidates by
  design; discovery and planning both need preserved diversity before a human
  chooses.
- **Generate plans automatically during discovery.** That spends inference
  before the operator has chosen a direction and makes the option screen slower
  for no decision benefit.
- **Store the plan as an artifact before approval.** Artifact status, diffs,
  reviews, and egress rules are the wrong machinery for a proposal that may be
  discarded; task creation on approval is the durable outcome.
