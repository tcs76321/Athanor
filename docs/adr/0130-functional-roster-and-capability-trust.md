# ADR 0130 — Functional roster and capability trust board

**Status:** Accepted · **Date:** 2026-11-08 · **Refs:** ADR-0049, ADR-0104,
ADR-0129; `internal/onboard/roster.go`, `internal/ui/roster.go`,
`internal/composition`

## Context

First use presented a three-card “baseline cast”: best small, best medium, best
big. It was a projection of only three of Athanor's five functional roles, and
it used a different score from the persona assignment layer. The result was
three notions of “best”:

- tall used a role-weighted capability score;
- main used overall score with a tiny top-band tiebreak and no real size
  penalty;
- wide used a size third and a separate role score.

On a real pool this produced tall = a role specialist, main = a large model
that should have been tall, and wide = a model almost as large as main. The
cards also hid context, speed, sample count, and family independence, and they
did not answer the operator's real question: which model is reliable for which
job?

Qualification already records per-capability evidence, and the engine already
selects per capability with context floors and an automatic exploration budget.
The UI needed to expose that evidence, not invent a parallel ranking.

## Decision

### 1. Five functions, not three size tiers

The starting cast is the five roles Athanor actually uses:

| Function | Role | Purpose |
|---|---|---|
| Worker | `main` | Everyday execution and synthesis |
| Architect | `tall` | Planning, decomposition, hard design |
| Scout | `wide` | Ingestion, repository reading, short summaries |
| Screener | `security` | Security screening, judgment, compaction |
| Second opinion | `alternative` | Independent candidate generation and exploration |

The UI shows all five, with model, size, context, measured speed, sample count,
quality, confidence, the reason for the choice, and the next candidates. The
same builder produces the persona assignments, so the displayed cast and the
applied cast cannot drift.

### 2. One utility per function

Each function uses a deterministic utility over one evidence vector:

- **quality** — displayed overall or the function's capability average;
- **confidence** — sample-weighted Wilson lower bound over the evidence,
  shown as the trust floor and capped by the measured quality;
- **context** — working target, with a soft score rather than an arbitrary tier;
- **speed** — measured decode tokens/sec, neutral until measured;
- **cost** — resident size relative to the fit budget;
- **independence** — inferred family separation.

Weights:

- **Architect**: quality and hard-reasoning coverage dominate; context and
  confidence next; size is allowed. A one-displayed-point quality band keeps a
  specialist with missing capabilities from outranking the overall leader.
- **Worker**: quality, speed, context, resident cost, confidence. The worker
  must be at least 35% of the architect's size, and candidates stay within one
  displayed percentage point of the best remaining model (rounded, so 81% can
  compete with 82% when it is meaningfully cheaper, but 80% cannot).
- **Scout**: speed and cost dominate, then confidence and quality. The scout
  must be at most 65% of the worker's size (relaxed to 85% only when nothing
  smaller exists).
- **Screener**: security, judgment, instruction, confidence, and a family
  outside the generator roles.
- **Second opinion**: independence, uncertainty, quality, context, and speed.

### 3. Capability leaders and experiments

Below the roster the UI shows, for every capability, the best model, its score,
sample count, runner-up, and gap. It also lists the models with the most useful
unexplored evidence (high uncertainty, different family, acceptable quality).

Exploration stays automatic and budgeted in the engine: the existing 10%
epsilon on familiar work and higher probability on novel work. Security
screening, HITL, and irreversible work never explore. The starting roster is
the trusted default; experiments are a separate, visible layer.

### 4. Speed and family evidence

Qualification now records Ollama's `eval_count` and `eval_duration` and stores
tokens/sec on the pool profile. Models qualified before this change show speed
as unmeasured until the next screen.

When the catalog reports only an architecture family (common for MLX and some
quantized builds), the model-name base is used as the lineage proxy
(`qwen3.8:27b-mlx` → `qwen3.8`, `bonsai-2:9b` → `bonsai-2`). The reported
family is the fallback when the name has no usable base.

## Consequences

- The roster is explainable and no longer depends on arbitrary size thirds.
- Worker and scout are meaningfully separated; the architect is the best
  measured depth choice.
- The capability board answers “who is reliable at this” directly.
- Speed is absent for pre-existing measurements until a re-run; the UI says so
  instead of inventing a number.
- Family inference is a proxy, not ground truth; the UI labels unknown families
  rather than claiming independence.
- The engine's per-call selector and automatic exploration remain unchanged;
  this ADR changes the starting cast and the evidence shown to the operator.
