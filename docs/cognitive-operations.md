# Cognitive Operations

**Status:** design note · **Date:** 2026-10-07 · **Refs:**
[ADR-0064](adr/0064-cognitive-operations.md) (the decision),
[ADR-0063](adr/0063-f4-closeout.md) (F4 closeout),
[ADR-0044](adr/0044-compute-policy-seam.md) (`policy` seam),
[ADR-0040](adr/0040-strategy-capture.md)/[ADR-0041](adr/0041-strategy-analysis.md)
(strategy records), [ADR-0045](adr/0045-verification-first-selection.md)
(verification-first); ARCHITECTURE §13, §17, §18.

This note catalogs the cognitive operations Athanor ships (or should), states
how each is verified and terminated, and describes the substrate that learns
which to invoke. It is the spine M8's ablation and selection build on. The
vocabulary is fixed in `AGENTS.md` §Terminology.

## 1. The model

An **operation** is a node in a job's execution graph. Every operation declares:

- **inputs** — what it consumes (task, candidates, context, prior results);
- **cost class** — S / M / L in model calls and wall time;
- **verification edge** — how its output is judged before it can affect the job
  (a deterministic verifier, a weak-but-tracked judge, or a human);
- **termination condition** — when it stops (budget, iteration cap, a passing
  verifier, or exhaustion);
- **containment class** — *internal-reversible* (proceeds autonomously) or
  *external/irreversible* (HITL-gated).

That set is **cognitive regulation**: the edges and termination conditions are
what keep structured iteration from becoming a spiral. Budgets throttle; they
are not ceilings on ambition.

## 2. Catalog

`V-edge` = verification edge; `Term` = termination; `Contain` = containment
class (I = internal-reversible, X = external/irreversible); `State` = today
(✅ present / ◐ partial / ✗ gap).

| Operation | What it does | V-edge | Term | Contain | Cost | State |
|---|---|---|---|---|---|---|
| **Fast-path** | small model / cache / deterministic check for easy cases | deterministic | first hit | I | S | ✅ `security` persona, verifiers |
| **Plan** | decompose the task; choose approach | criteria parse + human | plan emitted | I | M | ✅ `tall`, `internal/dag` |
| **Draft** | generate one candidate | verifier on output | one artifact | I | M | ✅ |
| **Diverge (best-of-N)** | generate N candidates @ temp>0 | verifier/rank over set | N or floor met | I | M·N | ✅ `internal/engine/diverge.go` |
| **Critique** | judge a candidate against criteria | deterministic first, judge advisory | verdict | I | S–M | ✅ `internal/verify` + judge |
| **Self-refine** | write → review → rewrite → perfect | verifier must accept the rewrite | ≤ k rewrites | I | M·k | ✅ M8-T11 (opt-in `self_refine`); repairs the best candidate in place under the `refining` phase |
| **Tree search / rollouts** | explore branches, back up the best | verifier per leaf | node/step budget | I | L | ⏸ deferred (M8-T12) |
| **Adversarial self-critique** ("nightmare") | generate failure cases, red-team own output | verifier on discovered break | case budget | I | M | ⏸ deferred (M8-T12) |
| **Scratchpad** | persistent working memory across phases | none (context only) | job end | I | S | ✗ |
| **Simulate** | counterfactual rollout against a model | oracle (for code: tests) | budget | I | L | ◐ tests-as-oracle only |
| **Consult gateway** | fetch allowlisted sources for grounding | reader + injection scan | per-source | I | S–M | ✅ ADR-0019 |
| **Daydream** | idle-time exploration + replay | draft-only, human review | AC+idle, budget | I | M | ✅ §17 |
| **Consolidate** | compact episodic/archival memory @ temp 0 | determinism test | memo written | I | M | ✅ MCE |
| **Forget** | expire / prune stale context and insights | policy | retention | I | S | ✅ insight expiry, compaction |
| **Accumulate habit** | promote a repeated pattern to a reusable rule/skill | human review + scan | promoted | I | M | ◐ corrections + insights; skills deferred |
| **Metacognise** | aggregate outcomes into process insights | deterministic math | threshold met | I | S | ✅ `internal/strategy` |
| **Commit / push** | persist an accepted artifact; publish | human approval (push) | committed | X on push | S | ✅ git-as-undo + HITL |

`X` appears once, at the machine boundary — which is the point: almost all
cognition is internal and reversible and therefore needs no human in the loop.

## 3. The learning substrate

Selection is learned from evidence, not hard-coded:

- **Trajectory store** — each terminal job records which operations ran, their
  cost, and the **grounded outcome** (tests, failing APIs, schema checks). This
  is `StrategyProfile`/`StrategyOutcome` extended so the outcome records the
  executed operations (M8-T7; `strategy_outcomes.operations_json`, migration
  0024).
- **Eligibility / selection** — `internal/policy` chooses operations from the
  trajectory evidence, task features, and remaining budget (M8-T8). Pure and
  deterministic; the default reproduces today's behaviour.
- **Grounded outcomes teach; human evals calibrate; the judge advises** — a
  technique that adds cost without moving a grounded signal is measurably not
  helping *for that class*. Human evaluations (the digest-meeting intake,
  M8-T13) are the durable teacher and the calibration for ambiguous checks.
- **Exploration floor** — idle/daydream sampling keeps unproven operations
  eligible, so a narrow history cannot starve the repertoire (M8-T10).

## 4. Novelty and the anti-rut reset

Learning creates a risk: the deeper the specialisation, the worse the agent may
be on a genuinely new problem. The reset rule (ADR-0064 §5):

1. **Detect novelty** — a new project or task class that lies far from history.
2. **Reset toward the default, not to noise** — selection returns to the
   *shipped default heuristics* (a good prior) **blended with exploration
   randomness**, never to uniform randomness.
3. **Explore directionally** — if the default path underperforms, step *away*
   from it along the learned gradient; remember the deviation.
4. **User option** — `novelty_reset: auto | reset | keep` per project;
   default reset-and-anchor-on-default once the agent has specialised.

This is the same pattern as every other regulatory path: a default, an edge,
a bounded deviation, a record.

## 5. Cognitive regulation (guardrails)

The "mental illness" failure modes and their regulators:

| Failure mode | Regulator |
|---|---|
| Rumination loop (identical tool calls) | loop detection (§18.1) |
| Non-termination (infinite reflection) | `max_reflection_loops`, phase budgets |
| Goal drift | mountains/rivers; static-prompt immutability |
| Confabulation | hallucination/path detection |
| Obsession (one correction dominates) | correction scope + weighting |
| Dissociation (context loss) | lossless division; byte-exact swap |
| Over-refinement | verification edge on every rewrite |

## 6. How M8 uses this

The M8 probe runs an **ablation**, not a verdict:

- **Arms:** single-shot · best-of-N · plan+reflect · full loop · (+ optional
  self-refine / adversarial / tree-search once built).
- **Strata:** easy (E) · medium (M) · hard (H) · multi-step-with-tools.
- **Metrics:** deterministic acceptance, anchor quality, cost, and
  **recovery-after-failure** (did a later operation fix an earlier failure?).

Read out per operation: keep, de-prioritise for a class, or build a stronger
form — always with the repertoire intact.

## 7. Deferred (M8-T12): adversarial self-critique and tree search

Both operations are real and worthwhile, and both are **deferred** rather than
implemented now, for the same reason: **their verification edge does not exist
cheaply yet, and shipping them without it would violate the cognitive-regulation
rule** (no operation without a verification edge).

- **Adversarial self-critique** produces *claimed* breaks. Counting a claim
  requires a way to *verify* it — for code, generating a stress test and running
  it in the Job Pod; for text, mapping the claim onto a deterministic structural
  check. Without that, the output is unverified LLM opinion, and capturing it as
  `CorrectionRecord`s would pollute the feedback store the whole system learns
  from. Enabling work: a generated-test → pod → grounded-break path.
- **Tree search / rollouts** is L-sized and its value is unmeasurable until the
  harder corpus (ADR-0061) exists to show where branching beats best-of-N.

Both stay in the repertoire (never pruned); neither is invoked until its edge is
real. This is the ADR-0064 rule applied to itself.

## 8. Open questions

- How is "novelty" measured on a local model without an embedding dependency?
  (candidate: task-feature distance + FTS5 similarity, no ML.)
- How aggressive should the exploration floor be before it costs more than it
  teaches?
- Which operations can share a verification edge, and which need their own?
