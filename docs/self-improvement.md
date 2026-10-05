# Local-First Self-Improvement — Fine-Tuning, Distillation, and Experience Reuse

**Status:** design note / hypothesis · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §13.4, §17, §18; ROADMAP §7; [F4 plan](f4-plan.md)

This note explores the natural next step past Athanor's current self-improvement:
weight-level learning. Athanor already improves at the **prompt** level
(`CorrectionRecord`s and `StrategyInsight`s). Nothing here is scheduled; it is a
scoped hypothesis with a clear safety boundary, intended to inform a future
milestone and to keep F4/M7 design decisions compatible with it.

## 1. Why Athanor is unusually well-positioned

Most systems that want to fine-tune an agent lack structured supervision.
Athanor is already accumulating exactly the corpus a post-training pipeline
wants, as a by-product of normal operation:

| Signal | Athanor source | Training use |
|---|---|---|
| Accept/reject outcomes | `StrategyOutcome.result`, `EvaluationRecord` | reward / preference labels |
| Rejections with a desired behavior | `CorrectionRecord.derived_rule` | KTO-style single-sided preference |
| Chosen vs rejected artifacts | accepted artifact vs losing candidates | DPO-style pairs |
| Process supervision | `security`-persona rubric verdicts (`passed`, `missing_criteria`) | process reward model |
| Which configs win | `StrategyProfile.signature` + outcomes | meta-level policy data |

The key insight: **this corpus is content-addressed and append-only already**, so
the data plumbing (dedup, provenance, replay) is half-built.

## 2. Techniques, and which actually fit

**Adapter fine-tuning (LoRA / QLoRA).** QLoRA (4-bit base + bf16 adapters) now
fits 7–8B models in ~8–12 GB and even a 27B in <22 GB. On Apple Silicon
(unified memory) it *works* but is memory-bandwidth-bound and slow versus a
CUDA GPU. Implication for Athanor: **fine-tuning is feasible on the target
hardware but is a slow, background, idle-time job** — a Daydreaming action
(§17), not an interactive one, and realistically the smallest personas first
(`wide`, `security`) rather than the 27B.

**Preference optimization (DPO family).** DPO trains on (prompt, chosen,
rejected) pairs. Two important variants for Athanor:
- **KTO** (Kahneman–Tversky Optimization) works with **single-response
  good/bad labels** — which is exactly the shape of a `CorrectionRecord`
  (a rejection with a desired behavior) and a `StrategyOutcome` (accepted or
  not). Pairing is not required.
- **SimPO** drops the reference model, simplifying the harness.

So the realistic first target is **KTO/DPO over Athanor's own accepted-vs-rejected
artifacts and corrections**, not a from-scratch SFT.

**Distillation.** A larger teacher (the `tall`/27B persona, or a cloud model
under HITL) generating accepted artifacts is a teacher signal for a smaller
student. Self-distillation (same family) is a compression path; cross-family
distillation is a capability-transfer path. This is attractive because it needs
**no human labels** — only accepted outputs — but it bakes in the teacher's
biases and can narrow diversity, which is bad for the dialectical loop's
candidate diversity (F4-T5).

**Process reward models / verifiers.** Distilling the `security` rubric into a
small verifier is high-leverage (F4-T3/T4): a fast, local, deterministic-ish
judge. But it is also the highest risk (see §3).

## 3. The safety boundary (mountains vs rivers)

The literature is unambiguous and sobering: **fine-tuning degrades safety
alignment, even on benign data** (catastrophic forgetting / alignment
regression), and a small amount of harmful or poisoned data can produce a model
that complies with it. For Athanor this dictates hard rules:

1. **The `security` persona is never trained.** Judgment and compaction run on a
   frozen, pinned model. A learned verifier may *advise*, but accept/reject and
   compaction stay on the frozen judge. (This is §4.2 "deterministic judgment"
   surviving weight-level learning.)
2. **Static security constraints are not in any trainable set.** They are
   prompt/data "mountains"; only rivers (style, scaffolding, domain tactics)
   are in scope.
3. **Replay + regularization + parameter isolation.** Adapters (not full
   fine-tunes) with a replay buffer of prior tasks and a safety-replay set;
   freeze safety-critical layers where possible.
4. **Every weight update is reversible and HITL-gated.** An adapter is a
   versioned *artifact* (draft), promoted only by explicit approval, with
   one-command rollback. A `drift` alarm (§22.3) fires if behavior shifts.
5. **The evaluation set is held out and frozen.** No training data may overlap
   the M3-T7 probe tasks or the acceptance suite (contamination control).

The honest summary: weight-level self-improvement is a **rivers** capability
that must be bolted to the **mountains** (frozen judge, gated promotion,
reversibility), not an excuse to relax them.

## 4. A staged pipeline (hypothesis)

1. **Data pipeline.** Export content-addressed preference/KTO records from
   `CorrectionRecord`s and accepted-vs-rejected artifacts; dedup by content
   hash; split a frozen eval set; store as a versioned dataset artifact with
   provenance (which jobs, which policy version).
2. **Offline eval harness.** Reuse the M3-T7 probe: run a candidate adapter
   against the frozen judge and the single-shot/dialectical baselines; measure
   quality-per-token, not raw quality (F4-T6).
3. **Shadow adapter.** Produce a candidate adapter as a **draft artifact**;
   never promote on training loss alone.
4. **HITL promotion.** A human approves promotion after seeing the eval; the
   adapter becomes the persona's model behind a config switch; rollback is one
   revert.
5. **Continuous replay.** Idle-time (Daydreaming) retraining with a replay
   buffer; the frozen judge audits every candidate.

## 5. Interaction with the roadmap

- **F4** should keep the compute/selection seam (`Policy`, verifier interface)
  compatible with a future *learned* verifier and *learned* persona defaults.
- **M7 Daydreaming** is the natural host: idle, AC-gated, budget-limited,
  draft-only output — exactly the shape of slow local training.
- A future milestone (informally `F5`/`M8`) could own: the data pipeline, the
  offline eval harness, adapter promotion, and the safety-replay set.

## 6. Non-goals and open questions

- **Not** full-parameter fine-tuning on the target hardware; adapters only.
- **Not** training the judge or any safety constraint.
- **Not** cloud training of the shipped personas (local-first); cloud may only
  act as a *teacher* under HITL, never as the deployed model.
- Open: how to detect *silent* capability regression beyond the eval set
  (the `drift` alarm's threshold); how to keep candidate **diversity** from
  collapsing after distillation (F4-T5's Jaccard floor is a natural guard);
  whether the corpus is large enough to beat a well-tuned prompt (the field's
  repeated lesson: *prompting first, fine-tuning when the data justifies it*).

## 7. Recommendation

Treat weight-level self-improvement as a **gated, reversible, idle-time**
capability with the frozen judge as the immutable boundary — and resist it until
the prompt-level channels (F4) are saturated and the corpus is large enough to
justify it. When it lands, it lands as adapters promoted by HITL, evaluated by
the same harness that measures everything else.
