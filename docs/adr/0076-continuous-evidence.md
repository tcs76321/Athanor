# ADR 0076 — Continuous evidence from real use, with independent labels

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
[ADR-0049](0049-model-onboarding-qualification.md) (onboarding/qualification),
[ADR-0064](0064-cognitive-operations.md) (operations),
[ADR-0067](0067-technique-guidance-loop.md) (technique guidance),
[ADR-0075](0075-frozen-corpus-compass.md) (frozen-corpus soak);
`docs/self-improvement.md`; AGENTS "repertoire vs. selection".

## Context

Athanor's selection layer (compute policy, routing, technique guidance,
escalation) learns from outcomes. If the only labels are the engine's own
verifier/judge, it **grades its own homework** — and real-use volume amplifies
that bias rather than correcting it. The scarce, independent signal is the
operator. Real use is also the only distribution that matters, but it is
confounded: the task mix drifts, there is no counterfactual, and most of it is
unlabeled.

## Decision

1. **Complete raw capture by default.** Every event, artifact, verdict, cost,
   diff, and timestamp is recorded automatically, with provenance (job →
   project → task → model/config). No per-task tagging action is required.
2. **Labels are derived, not typed.** The evidence corpus joins raw capture to
   independent signals: deterministic tests/checks; downstream reality (was the
   artifact committed, used, edited, re-run, abandoned?); implicit operator
   actions; and explicit corrections **when offered**. A correction UI exists
   but is opportunistic, not the primary channel.
3. **Periodic human eval sessions are the anchor.** A sampled, **blinded**
   review queue (artifact/diff/cost, no model name) scored against a short
   rubric, joined back to provenance. This calibrates the judge and anchors
   selection. It replaces per-task tagging as the main human signal.
4. **The judge is never a label.** The LLM judge is advisory; it may not be
   used as ground truth for tuning.
5. **Selection vs. validation.** Real use feeds **selection** (routing, budgets,
   escalation, prompts). The frozen-corpus soak ([ADR-0075](0075-frozen-corpus-compass.md))
   **validates** it. No improvement claim rests on real use alone.
6. **Deterministic tuning, not weight training.** Evidence tunes Athanor's
   deterministic layer — which model per role/class, phase budgets, escalation
   rungs, verifier thresholds, prompt/context policy, retry ceilings. Weight-level
   learning stays gated, reversible, and later (`docs/self-improvement.md`).
7. **Onboarding measures before it routes.** The shipped qualification ladder
   enumerates the operator's installed models, runs a bounded grounded
   qualification, and proposes a per-role routing table for approval — so first
   use is measured against *their* models, not a fixed roster.
8. **Idle adaptation is a background tenant.** Daydreaming, minting evidence, and
   model qualification are preemptible, deferred, budgeted, and reversible; they
   never tax the next interaction (evicting a resident model is a real cost).

## Consequences

- The evidence pipeline must reduce **any daemon state dir**, not just the
  probe's results tree, so real use and soaks share one report path.
- A review-queue surface and a ratings store are first-class deliverables.
- Retention/compaction and a held-out separation are required: complete capture
  is not the same as a clean training set.
- Privacy and storage are explicit costs; provenance makes selective deletion
  possible later.
- The selection loop can be evaluated two ways — real-work movement and
  held-out-corpus non-regression — which is the anti-self-grading guard.

## Alternatives rejected

- **Explicit per-task rating as the only human signal.** High friction, sparse,
  and biased toward what the operator happens to notice.
- **Self-labels (the engine's accept/reject) as ground truth.** Circular at any
  volume.
- **Fine-tuning first.** The field's lesson is prompting/policy first; the data
  must justify weight updates.
- **Treat real use as the validation set.** No counterfactual, no held-out
  control, so improvement is unfalsifiable.
