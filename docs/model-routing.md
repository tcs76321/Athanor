# Model Routing — Combinations Over a Single Model

**Status:** design note / thesis · **Date:** 2026-10-05 · **Refs:** [model tiers](model-tiers.md), [LOM/LAM + RLVR](lom-lam-integration.md), [self-improvement](self-improvement.md), [F4 plan](f4-plan.md), [M3-T7 results](probes/m3-t7-quality-probe-results.md)

The system should not be "one model, everywhere." It should choose **which
model, for which phase, for this user, right now**, and learn that choice from
its own outcomes. This note argues the case, maps it onto what Athanor already
has, and states the guardrails.

## 1. The probe made the case for us

M3-T7 (see the [results](probes/m3-t7-quality-probe-results.md)) found that the
dialectical loop **generates** diverse candidates (mean pairwise Jaccard 0.795)
but **cannot rank them**: the verifier is the *same model* as the generator, so
its errors are correlated and best-of-3 collapses toward best-of-1. The
third-party judges were worse — 22%/51% error rates and a saturation at 1.0.

So the bottleneck is not raw generation. It is **judgment**, and a single-model
system cannot fix judgment with more of the same model. The fix is a
**combination**: a verifier from a different family, and per-phase model
selection. That is routing.

## 2. The durable value is not the model

Restated from the [LOM/LAM note](lom-lam-integration.md): weights are perishable;
the substrate and the evaluation history are not. A new local model (the next
Qwen-9B) replaces a base model in a day. What carries forward:

- the deterministic substrate (pods, gates, tool envelope, schema, DAG);
- the **evaluation corpus and corrections** — the portable teacher.

The routing policy is a thin, **re-learnable** layer over that durable core.
Personalization is therefore not a feature bolted on; it is *what the durable
core accumulates*, and it survives every model generation.

## 3. The unit of choice

Not "the model" — the tuple **(phase × model × persona × params)**. Athanor
already has the phases (`planning`, `diverging`, `evaluating`, `reflecting`,
`synthesizing`, `comparing`) and already records this tuple per job in
`strategy_profiles`. Selecting it per phase is the whole idea:

| Phase | Wants | Example composition |
|---|---|---|
| planning | broad world knowledge, instruction-following | `tall` model |
| diverging | cheap, fluent, varied | fast local generator |
| evaluating | **independent judgment** | a *different family* than the generator |
| reflecting | self-critique | main model |
| synthesizing | writing quality | main / best writer |
| comparing | decisiveness, calibration | independent judge |
| tool-calling | function-call accuracy, small | a tool-trained model (Granite-class) |

## 4. Athanor is already logging the training set

`strategy_profiles` (context: archetype, task features, signature) →
`strategy_outcomes` (reward: accepted/rejected, score, confidence, tokens, wall
time). That is a **contextual bandit**: state = task features, action = the
model/persona config, reward = the outcome. No new infrastructure is required to
start — a bandit is arithmetic, replayable, and reversible. This is the concrete
meaning of "learn slowly, in the right direction," and it needs **no GPU**.

That also reframes **F4-T1**: the compute `Policy` is not only a budget — it is
the **model-selection policy**. See §8.

## 5. Compositions to support

1. **Per-phase routing** (§3) — the baseline.
2. **Heterogeneous divergence** — draw candidates from *different* models, not
   N samples of one. The divergence structure already supports it.
3. **Independent verifier** — evaluate with a model from a different family
   (or a distilled verifier); decorrelated error is the point of the probe's
   negative result.
4. **Tool-caller as a separate tier** — a small function-calling model for the
   closed tool space (the LAM); don't make the writer model also route tools.
5. **Cascade / escalation** — cheap model first, escalate to a stronger (or
   cloud) model only on low confidence or repeated failure; HITL for the rest.

## 6. Guardrails (mountains vs rivers)

Routing is a **river**. It may evolve; the mountains may not move:

- The router **cannot** bypass the per-job envelope, containment gates (G1/G2),
  or HITL for external/irreversible actions.
- The **frozen judge gates** accept/reject; a learned verifier *advises*.
- **Promotion ratchet:** every change to routing (or a fine-tune) is gated by
  *eval-must-beat-incumbent → reversible → drift alarm*. Without the ratchet,
  "learning" can drift down.
- **Exploration:** keep ε/Thompson-style exploration so the policy doesn't
  collapse to one model, plus the F4-T5 Jaccard floor against diversity collapse.
- **Reward source:** learn only from verifiable signals and human verdicts, never
  from the LLM judge's verdict (see RLVR in the [LOM/LAM note](lom-lam-integration.md)).

## 7. Learning ladder

- **L0** static config (today)
- **L1** contextual bandit over (phase, model, persona) ← *start here; no ML*
- **L2** prompt/retrieval personalization from corrections + memory
- **L3** distilled independent verifier
- **L4** DPO/KTO fine-tunes of specialists
- **L5** GRPO/RLVR (divergence cycle = GRPO group)

Each layer feeds the next, and each is gated. Do not start L3–L5 before L1 has
accumulated enough outcome data to learn from.

## 8. The ADR

This reframe is recorded in [ADR-0044](adr/0044-compute-policy-seam.md), now
"Model-selection policy as an explicit seam (F4-T1)": `Policy.Decide(features,
context) → Plan{candidates, model routing, reflection loops, judge mode/count}`,
defaulting byte-identically to today's behavior. The routing table is sourced
from `strategy_outcomes`.

## 9. Open questions

- How much outcome data before the bandit beats a hand-set routing table?
- Does heterogeneous divergence beat homogeneous divergence on *verified* quality
  once a real verifier exists?
- How to keep the router honest as base models churn (re-validate on model
  upgrade, not just at rollout)?
- Who owns the routing table: learned data, or user-editable config? (Same
  rivers-vs-mountains choice as ontology governance.)
