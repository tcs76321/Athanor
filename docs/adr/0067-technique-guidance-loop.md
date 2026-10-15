# ADR 0067 — The technique-guidance learning loop

**Status:** Accepted · **Date:** 2026-10-15 · **Refs:**
ARCHITECTURE §13.3/§13.4; [ADR-0064](0064-cognitive-operations.md)
(cognitive operations); [ADR-0063](0063-f4-closeout.md) (G8);
[ADR-0044](0044-compute-policy-seam.md) (policy seam);
[ADR-0041](0041-strategy-analysis.md) (strategy analysis);
[ADR-0040](0040-strategy-capture.md) (strategy capture); `AGENTS.md`
§Terminology (repertoire vs. selection); [cognitive-operations.md](../cognitive-operations.md)

## Context

Athanor's self-improvement has two halves. The **feedback system** (§18)
captures *prescriptive* lessons from humans ("do not do X"). The **strategy
analysis** system (§13.4) is meant to mine *descriptive* lessons from aggregate
outcomes ("approach Y is accepted more often on code tasks"). Two pieces exist:

- **Capture.** Every terminal job records a `StrategyProfile` (at start) and an
  immutable `StrategyOutcome` (at end). M8-T7 added the job's **executed
  cognitive trajectory** — the ordered operations that ran, each with attributed
  cost and a **grounded** verdict where a deterministic verifier judged it
  (`strategy_outcomes.operations_json`).
- **Mining.** `strategy.Mine` groups outcomes into cohorts and emits proposed
  `StrategyInsight`s. But it groups only by **persona** and **candidate count**
  (`phase.persona`, `diverging.candidates`); it never looks at the trajectory.
  Proposed insights are inert until promoted, and promotion is HITL-gated
  (`strategy_analysis.auto_promote` defaults off).

M8-T8 introduced a `Plan.Operations` eligible-set surface, with the follow-up
(M8-T8b) framed as *narrowing* that set on easy tasks. That framing conflicts
with the project's stated model — **the repertoire ships whole; what is learned
is selection (when, where, and for whom)** — and narrowing risks starving an
unproven operation.

The intended behaviour is different: **analyse when something went right, see
where and how, condense it, and feed that back into future attempts** — starting
with a technique. That loop does not exist today.

## Decision

**1. Supersede M8-T8b-as-narrowing.** The learned layer is an **advisory
technique-guidance channel**, not a narrowing of the eligible operation set.
`Plan.Operations` remains the runnable set (the baseline minus hard constraints
such as zero reflection budget); guidance never removes an operation.

**2. Mine technique-level wins from the grounded trajectory.** For a task class
(archetype), the system correlates each **operation** with **grounded success**
(deterministic verifier pass, real-test pass) over recent outcomes, and emits
winning technique insights. Aggregation is deterministic and performs no LLM
call; the statement is rendered from the computed numbers, so nothing is
invented. This is the §13.4 "detection" rule applied to the operation axis
rather than the persona axis.

**3. Condense into a short technique guidance line.** A technique is
{operation, when-to-apply context, evidence}. Active winning techniques render
as one short, versioned guidance line injected at the existing strategy-notes
channel (§11.2 position 14), and every application is audited
(`strategy_notes_injected`). Guidance is advisory prose in the prompt — it does
not alter the eligible set, the phase plan, or any safety constraint.

**4. Auto-activate winning techniques above a strict bar.** Winning technique
insights whose grounded evidence clears a bar (cohort floor and a positive
grounded-accept delta, stricter than the general proposal threshold)
auto-activate so the loop closes without operator babysitting. Activation is
bounded (`strategy_analysis.max_active_insights`), scoped (project/global),
reversible (`mute`), and audited. Losing insights and the persona-plan bias
keep the existing HITL gate — only *grounded winning techniques* auto-activate.

**5. Keep the exploration blend.** The novelty reset (M8-T9) still drops learned
bias on a novel class toward the shipped default heuristics; the exploration
floor (M8-T10) still keeps an unproven operation runnable. Because guidance is
advisory, an unproven operation is never starved by it.

**6. Mountains and rivers.** Guidance never modifies static prompts, security
policy, containment, the frozen judge, or HITL rules. It biases defaults and
adds advisory prompt lines only. This is the "river" side of the model.

## Consequences

- G8's "selection is learned from grounded outcomes" clause is satisfied by a
  **demonstrated loop** — techniques mined from grounded trajectories, activated,
  and injected — with an **observed grounded delta** reported on the hard
  corpus. A claim of *statistical* improvement is explicitly post-0.0.0, pending
  measurement power ([ADR-0066](0066-release-scope-soak.md)).
- The canonical operation vocabulary (`internal/cognitive`) is the technique
  namespace; techniques are relationships among operations, not a new
  capability surface.
- `Plan.Operations` is documented as the eligible set that guidance does not
  modify; M8-T8b is retired as a narrowing task and replaced by this channel.
- ARCHITECTURE §13.3/§13.4 are updated to name the technique axis.

## Alternatives rejected

- **Implement narrowing (M8-T8b as planned).** It prunes selection, can starve an
  unproven operation, and contradicts "the repertoire ships whole."
- **Keep all promotion HITL-gated.** It is too slow to close the loop during a
  single validation run; a strict, reversible, audited auto-activation for
  grounded winning techniques is the proportionate default.
- **Inject raw, unsupervised free text from the model.** Guidance is rendered
  deterministically from the canonical vocabulary and inline evidence, at
  Temperature 0.0 where any phrasing pass is used — never a free-form model
  claim in the prompt.
