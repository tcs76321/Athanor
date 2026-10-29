# ADR 0090 — Task analysis sets a contemplation budget the loop revises

**Status:** Proposed — S0–S1 implemented; S2–S4 open · **Date:** 2026-10-29 ·
**Refs:** ARCHITECTURE §8.2 (state rules), §13.1 Phase 1 (planning), §19.3
(comparison), §24 (power and idle policy); [ADR-0044](0044-compute-policy-seam.md)
(compute-policy seam), [ADR-0064](0064-cognitive-operations.md) (operation
vocabulary), [ADR-0074](0074-attempt-escalation.md) (attempt escalation),
[ADR-0077](0077-two-day-cycle.md) (continuous operation);
`docs/qualification-smoke-m9.md` (planner self-assessment),
`docs/probes/gf4-closeout.md` (level-based thinking),
`docs/probes/m3-t7-quality-probe.md` (runaway reasoning).

## Context

Athanor currently treats model reasoning as **one job-level switch**.
`inference.think` is a `*bool`, default `false`; the engine forces it off for the
judgment phases and otherwise applies the configured value to every generation
call (`phases.go:422`). The decision is made once per call from configuration,
not from the task.

The loop has no place to make a better decision. Phase 1 (planning) asks the
`tall` persona for prose plus a `DIFFICULTY: easy|hard` hint. The prose is
advisory and **not persisted** (`phases.go:697`); only the hint is stored. Phase
4 (reflection) produces a counter and a prose proposal for the next divergence
pass. Nothing distinguishes *the execution was wrong* from *the frame was wrong*,
and nothing attributes a failure to the part of the task that caused it.

Three measured findings constrain any design here:

1. A thinking-capable model can spend the entire `num_predict` budget on hidden
   reasoning and emit empty visible content (M3-T7; the runaway that made
   `think` default off).
2. A weak planner called hard tasks easy often enough that the policy now
   treats repeated failure as evidence of difficulty (`HardByEvidence`, M9-T4).
   The planner's self-assessment is a signal, not an authority.
3. `think:false` is honored by boolean-thinking models but interacts badly with
   qwen's level-based interface, producing empty outputs (G-F4 closeout). A bool
   cannot express "how much" even where the server supports it.

So neither "always think" nor "let the planner decide" is safe. The loop needs a
budget it can spend on evidence and revise when the evidence changes — including
the case where the task looked easy and only later proves wrong.

**A soak is a measurement instrument, not the operating mode.** Real Athanor runs
overnight as a stream of interleaved jobs — interactive work, memory
consolidation, indexing, daydreaming — under sleep/wake and power policy. A soak
is a bounded batch of independent jobs with a fixed pass count and a wall-clock
cap, so a design that treats the job as the top-level budget can look correct in
a soak and be unbounded in a night. This ADR therefore scopes contemplation
inside a **horizon** budget (see item 11); the per-job allowance below is an
allocation, not a fresh pot.

## Decision

1. **Phase 1 is the analysis phase.** Planning keeps its state and temperature;
   its output becomes a structured `TaskAnalysis` persisted with the job. It
   emits, in addition to the plan and the `DIFFICULTY` hint:
   - **units** — the task decomposed into the parts that can be worked
     independently, each with an id and a goal;
   - **mode** — `direct` or `deliberate` per unit, with a reason;
   - **falsifier** — the cheapest check that would show the unit is wrong.
   An unparseable analysis degrades to **one `direct` unit** covering the whole
   task. There is no retry loop for analysis.

2. **Units are addressing labels in the first slice, not separate jobs.** A unit
   does not get its own divergence pass. It gives the budget and the retry a
   target: a failure attributed to unit 2 produces a directive about unit 2.
   Executing units independently is a later DAG decision and explicitly out of
   scope.

3. **Default is `direct`; deliberation is earned.** A unit becomes `deliberate`
   only when analysis flags it as uncertain or high-risk, or when a grounded
   signal (verifier failure, repeated failure signature, low confidence) fires.
   Analysis *proposes* the budget; the policy clamps it to operator ceilings;
   grounded evidence overrides it.

4. **Thinking is a dimension of a unit's execution, not a plan axis.** The plan
   carries only the ceiling (`Reasoning: {mode, level, reserve_tokens}`). The
   live budget is job state: per-unit mode, remaining deliberate allowance,
   reasoning tokens spent, and the last failure signature. It is persisted in
   `system_state` (`analysis:<job-id>`, `contemplation:<job-id>`), like the
   reflection counter, so a resumed job keeps its budget. No migration.

5. **The regulator is the evaluation checkpoint.** When no candidate passes,
   reflection returns a decision instead of only prose:

   | Decision | Condition |
   |---|---|
   | `settle` | the unit's falsifier passed; stop spending on it |
   | `refine(unit)` | failure is attributed to a unit that has not been refined |
   | `reframe` | the same failure signature repeated, or the failure is frame-level |
   | `probe` | the failure reason is ambiguous; run the cheapest check first |
   | `defer` | the horizon budget is spent right now; keep the state and resume later |
   | `abandon` | effort is exhausted or the frame is wrong and out of budget; fail with a reason |

   `defer` and `abandon` are different outcomes. A job that runs out of *night*
   is not a failed job; a job that runs out of *ideas* is.

6. **Refine is capped; then reframe.** At most one `refine` per unit per job.
   A second identical failure is evidence the frame is wrong, and more thinking
   inside the same frame has low expected value. The response is a different
   frame (persona, divergence, or model escalation), not a larger reasoning
   allowance. `ADR-0074` attempt escalation remains the fallback when analysis
   is absent.

7. **Probe before deliberate.** A unit's falsifier is run when it is cheap and
   available (for `code`, the deterministic verifier on the implicated case)
   before deliberation is granted. Introspection cannot reliably detect a wrong
   answer; a cheap external check can. When no probe exists, the unit stays
   `direct` unless analysis flagged it.

8. **Judgment is never eligible.** `evaluating` and `comparing` stay forced off.
   Analysis cannot enable reasoning for them, and the regulator cannot override
   that. `inference.think` becomes a ceiling — `off` (default) | `auto` | `on` —
   where `off` preserves today's behavior exactly. Slice 1 has no effect until
   an operator selects `auto`.

9. **The wire must express level, not just bool.** A prerequisite, separately
   committable change replaces `Request.Think *bool` with a mode that can send
   `false` or `"low"|"medium"|"high"`, records declared per-persona capability,
   and accounts reasoning tokens separately from visible output. Without that
   accounting "how much" is unobservable and no later selection can learn it.

10. **Every decision is auditable.** `task_analysis`,
    `contemplation_spend`, and `regulator_decision` events record the unit,
    decision, reason, and tokens, so the spend can be attributed after the fact.

11. **Budget scope is horizon-first.** Contemplation draws from three nested
    scopes, and only the outermost is a real limit:

    | Scope | Controls | Refills |
    |---|---|---|
    | per call | `max_output_tokens`, phase wall time | new call |
    | per job | deliberate units, attempts, reflection | new job |
    | **per horizon** | tokens, wall time, energy across all jobs | time (a night, a day, a week) |

    A job's allowance is **allocated from** the remaining horizon budget; it is
    never an independent pot. The horizon budget is a rate plus a reserve, not a
    single number: an interactive reserve that background work may not touch,
    a background share that may be preempted, and an absolute ceiling that is
    raised only by an explicit operator action or an earned, bounded grant. It
    never compounds automatically.

    When the horizon budget is spent, background and daydream work **defers**
    (state kept, requeued later); interactive work may ask the operator to
    extend. Exhaustion is not a job failure. The horizon budget itself — fields,
    defaults, preemption, and accounting — is
    [ADR-0091](0091-horizon-budget.md), because it governs indexing and
    daydreaming too, not only contemplation.

## First slice

**S0 landed (2026-10-29):** `llm.ThinkMode` replaces `Request.Think *bool`, so
the wire can carry `"low"|"medium"|"high"`; `inference.think_level` selects it;
`Response.Thinking`/`ReasoningTokens` record the returned trace (an estimate —
Ollama's `eval_count` includes reasoning); judgment phases remain forced off.
Behavior is unchanged unless `think_level` is set.

**S1 landed (2026-10-29):** planning emits a fenced `analysis` block; the
lenient parser (`internal/analysis`) produces units with a **default-direct**
mode and a falsifier, and the engine persists it in `system_state` with a
`task_analysis` audit row. A malformed block degrades to one direct unit. The
analysis is not yet consumed by the regulator; S2–S4 remain open.

| Step | Change | Behavior |
|---|---|---|
| S0 | level-aware think mode + reasoning-token accounting | none; fixes the qwen confound |
| S1 | `TaskAnalysis` parse, persist, default-`direct`, budget state | none until `think: auto` |
| S2 | regulator classification + failure attribution + one-refine cap | `auto` only |
| S3 | falsifier execution for `code` units | `auto` only |
| S4 | arm-tagged outcomes and per-model/class reasoning estimates | learns |

Tests required by S1/S2: analysis parse and fail-safe single unit; default
`direct`; judgment exemption; `refine` once then `reframe` on a repeated
signature; horizon exhaustion **defers** while effort exhaustion **abandons**; a
resumed job keeps its spent allocation; `think: off` produces byte-identical
request flags to today.

Metrics to report before any claim: deliberate-unit spend per job, refine→pass
rate, reframe→pass rate, and **wasted deliberation** (deliberate units that were
already correct). A policy that thinks more and passes the same tasks is a cost
regression, not an improvement. Across a night, the metric is accepted outcomes
per unit of horizon budget, plus interactive latency — not pass rate on a fixed
six-task slice.

## Consequences

- Default behavior is unchanged. The whole design is inert until `think: auto`
  (or `on`), so it cannot silently change benchmark results.
- The analysis call replaces the existing planning call; it does not add a
  phase or a model call.
- Units are coarse and analysis-declared. A mis-decomposed task yields a
  mis-targeted directive; the one-refine cap and the `reframe` path bound the
  damage.
- Failure attribution is only as good as the verifier's reason. A generic
  "tests failed" leaves the regulator at frame level, which is the safe default.
- Persisting analysis in `system_state` keeps the schema stable; if analysis
  becomes queryable evidence, it should be promoted to a column or table with a
  migration rather than parsed out of JSON.
- The regulator stays deterministic: a pure classification over the analysis,
  the verifier reasons, the failure signature, and the remaining budget. No
  extra model call is needed to decide.
- **A per-job allowance is not a budget.** Without the horizon layer, a long
  night multiplies the allowance by the job count and the policy looks cheaper
  than it is. S1 must read the horizon allocation before it grants anything;
  implementing S1 against a job-local counter would bake in the soak shape.
- The frozen corpus remains the compass, not the objective. A change that helps
  six X-code jobs but starves overnight interactive work is a regression even if
  the corpus pass rate rises.

## Alternatives rejected

- **A separate reasoning axis on the plan, alongside candidates and reflection.**
  It cannot express "this part needs it and that part does not," and it makes the
  budget a decision instead of a resource the loop spends and revises.
- **The job as the top-level budget.** It is the soak's unit, not the product's.
  A night of jobs would each get a fresh allowance, and budget exhaustion would
  read as failure instead of deferral.
- **Always-on thinking.** Known empty-output and cost failure modes (M3-T7,
  G-F4); no termination signal.
- **The planner's budget is authoritative.** M9-T2b shows weak planners
  mislabel difficulty. The planner proposes; evidence overrides.
- **An attempt-only escalation ladder.** Cheap to build, but it spends on a bad
  frame and cannot attribute failure, so it cannot tell "try harder" from "try
  differently."
- **Prompt-level self-regulation ("think if needed").** A thinking model reasons
  whether or not the prompt asks; there is no enforceable cap and no
  verification edge.
- **Answer a repeated failure with more thinking.** The same frame produces the
  same error; diversity or escalation is the correct response.
