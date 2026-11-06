# ADR 0094 — Project discovery is a portfolio, not a dialectical winner

**Status:** Accepted — first implementation slice; amended by
[ADR-0097](0097-ai-plan-candidates.md) · **Date:** 2026-11-04 ·
**Refs:** ARCHITECTURE §15 (brainstorming workflow), §13 (dialectical engine),
§19 (evaluation), §21.5 (gateway); [ADR-0032](0032-dag-decomposition.md),
[ADR-0061](0061-harder-benchmark.md), [ADR-0093](0093-project-capabilities-not-archetypes.md).

## Context

An exploratory goal ("make money online", "find a small B2B service", "what
should I build next") needs a **set** of distinct directions before a plan is
worth writing. The architecture's §15 brainstorming workflow calls for exactly
that: questions and assumptions, multiple directions, constraints, scoring, a
selection, and a proposal artifact with strengths, weaknesses, risks, and next
tasks.

The dialectical engine cannot serve this need. Its divergence/evaluation/
comparison phases generate several candidates and deliberately **keep one
winner**. That is correct for producing an artifact, but it is wrong for
ideation: it discards the diversity the operator is trying to see. A goal that
falls through the generic pipeline also inherits code-centric planning language
("tests and documentation updates") and a single-document rubric.

## Decision

1. **Discovery is a separate, portfolio-producing stage.** It lives in
   `internal/discovery` and is invoked by the project onboarding flow, not by
   the engine's single-winner comparison.
2. **The operator briefs first.** Before discovery, the project records a short
   goal (three characters minimum), constraints/limits, an explicit "must not
   do" list, success criteria, and the sources Athanor may read. The brief is
   durable and resumable.
3. **Discovery returns a scored portfolio.** A bounded model pass produces a
   JSON array of concrete options across at least four industries, with
   customer, pain, wedge, business model, pricing, first offer, validation
   experiment, cost, time-to-first-revenue, skills, competition, evidence,
   risks, assumptions, and confidence. Deterministic validation rejects
   malformed or non-diverse portfolios; deterministic scoring exposes clarity,
   evidence, speed, capital fit, and risk. A session is durable from the moment
   it starts: a Core restart leaves a `generating` or `failed` state that the
   UI can show and retry instead of silently dropping the run.
4. **Evidence is honest.** Only human-approved sources are fetched through the
   §21.5 gateway. A claim without a source stays a claim; the UI shows
   confidence separately from the score. The model is told not to invent
   market sizes or citations.
5. **The human chooses.** Selecting an option is a recorded project decision.
   Approval generates a 30-day plan and a dependency-ordered task graph. The
   operator can re-run discovery or choose a different direction. **Amended by
   [ADR-0097](0097-ai-plan-candidates.md):** the operator may ask the model for
   three plan candidates and select one; the deterministic plan remains the
   fallback.
6. **The engine still executes.** Discovery does not accept artifacts or bypass
   verification; the plan's tasks go through the normal engine, Job Pods,
   verifiers, and HITL gates.

## Schema

Migration 0034 adds `project_briefs`, `discovery_sessions`, and
`discovery_options`. It also rebuilds `goals` with a 3-character floor so a
short, honest goal is valid; the onboarding brief supplies the depth.

## Consequences

- Exploratory goals get a useful first step instead of a premature document.
- The single-winner engine remains unchanged for execution.
- Discovery is a new model surface with its own prompt, validator, and cost;
  it is bounded, auditable, and optional.
- A local 9B may still produce generic ideas. The diversity/industry checks and
  the human selection are the guardrail; an ideation eval is the measurement.

## Amendment (2026-11-06)

The direction model is generalized beyond business framing. Migration 0036
renames the option columns to `domain`, `focus`, `audience`, `need`, `approach`,
`format`, `availability`, `first_deliverable`, `first_check`,
`time_to_first_deliverable_days`, `setup_cost_usd`, and `alternatives`.
Portfolios now vary within the goal's subject instead of requiring four
unrelated industries, and plans are first iterations rather than 30-day
business sprints.

## Alternatives rejected

- **Reuse divergence/comparison.** It selects one winner, the opposite of a
  portfolio.
- **Ask the model for one "best business idea".** One answer hides the trade
  space and invites false confidence.
- **Let the model browse arbitrary URLs.** It violates the gateway allowlist
  and the human-approval boundary.
- **Create a new project archetype.** Capabilities plus an onboarding brief
  describe the intent without reintroducing the taxonomy ADR-0093 removed.
