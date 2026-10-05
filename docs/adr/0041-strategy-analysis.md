# ADR 0041 — Strategy analysis engine (M6-T11)

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §13.4,
§17.1, §27, §22.3; ROADMAP M6-T11; ADR-0040 (strategy capture).

## Context

M6-T10 captures a `StrategyProfile` and an immutable `StrategyOutcome` per
job. §13.4 turns those into `StrategyInsight`s by deterministic aggregation:
group outcomes by strategy features, compare a cohort's accept rate against a
baseline, and propose a winning/losing pattern with inline evidence. Insights
are **advisory**: proposed insights are inert until a human promotes them,
and they never modify static prompts, security policy, or rubrics.

## Decision

### 1. Aggregation is pure and invents no numbers

`strategy.Repo.Mine` loads outcomes joined with their profiles and:
- computes a baseline accept rate per archetype;
- builds cohorts keyed by `(feature, value, archetype)` for
  `<phase>.persona` and `diverging.candidates`;
- keeps a cohort only when `cohort_jobs >= min_cohort_size`,
  `|accept_rate − baseline| >= min_accept_rate_delta`, and the mean
  evaluator confidence moves in the same direction;
- writes a `proposed` insight whose statement is rendered **from the
  numbers** (`f=v on <archetype> tasks: accept rate 0.80 vs 0.60 baseline
  (n=20).`) — there is no LLM in the detection path, so §13.4's "never
  invents numbers" is structural.

Two §13.4 refinements are simplified and documented: the "no larger
containing cohort contradicts it" guard is approximated by the per-archetype
baseline, and insights are emitted at `global` scope only (project scope
needs a larger corpus to be meaningful).

### 2. Proposed insights are provably inert

`ActiveStatements` returns statements of `active` insights **only**. The
engine's `SetStrategyInsightSource` seam injects them at §11.2 position 14 /
§10.5 tier 7 (gated by `strategy_analysis.strategy_notes_in_prompts`, audited
`strategy_notes_injected`). Because the source cannot return a proposed
insight, a proposed insight cannot reach a prompt — and
`TestProposedInsightDoesNotAffectPrompt` proves it end-to-end: the marker is
absent while proposed and present after activation.

### 3. Promotion is HITL-gated and logged

`POST /strategy/insights/{id}/promote` calls a cmd-side promoter: with
`strategy_analysis.auto_promote` (default **off**) it activates directly;
otherwise it files a `strategy_insight` HITL request and the insight stays
`proposed`. The registered approver (`insightActivator`) activates it only on
approval, and every decision is already logged by the HITL queue (M6-T4).
Muting is safe and direct.

### 4. Operator surfaces

JSON routes (`GET /strategy/insights`, `POST /strategy/mine`,
`.../promote`, `.../mute`), `athanor strategy mine|insights|promote|mute`,
and a read-only Statistics panel in the UI showing outcomes plus insight
evidence. Persona-plan bias, template ranking, and ExplorationPath proposals
(§13.4's other application channels) remain future work; the notes channel is
the implemented one.

## Consequences

- A synthetic corpus yields the expected winning/losing insight, and re-mining
  does not duplicate a live insight (`TestMineSyntheticCorpus`).
- The advisory boundary holds: proposed ≤ inert, promotion requires approval
  unless the operator opts into auto-promotion, and the promotion path is
  audited.
- The engine grows one nil-safe seam; no new dependency, Gate G1 unaffected.

## Implemented (M6-T11)

`internal/strategy/analysis.go` (`Mine`, `CreateInsight`/`GetInsight`/
`ListInsights`/`SetInsightStatus`/`ActiveStatements`); the engine
`StrategyInsightSource` seam and strategy-notes injection/audit; the
HITL-gated promoter + approver; API routes; `athanor strategy`; and the UI
Statistics panel.
