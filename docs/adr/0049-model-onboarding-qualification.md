# ADR 0049 — Model onboarding & qualification (proposed)

**Status:** Proposed — not implemented · **Date:** 2026-10-05 · **Refs:**
ARCHITECTURE §12 (personas), §12.3 (context feasibility), §12.5 (model
lifecycle), §13.4 (strategy analysis); ROADMAP §7 backlog; [F4 plan](../f4-plan.md);
[docs/model-routing.md](../model-routing.md)

## Context

Athanor's personas are configured by hand (`config.yaml`), but the machine's
capability changes: models are pulled and removed, base models are upgraded,
and RAM/VRAM headroom varies. The M3-T7/F4 work showed model choice matters
enormously — the same-family judge saturated while a cross-family judge
recovered real agreement — and the project already records the evidence
(`strategy_outcomes`, `internal/judge` anchor agreement).

The user asked whether Athanor should take a list of available models, work out
what fits in memory, and run a qualification pass (e.g. overnight) to discover
which model should serve which role — once at setup and/or periodically.

## Decision (proposed)

Add a **model onboarding & qualification** capability:

1. **Enumerate** the models available to the configured backend (e.g. Ollama
   `/api/tags`) and the ones named in config.
2. **Feasibility** per §12.3: compute the max context each can run at from
   available RAM/VRAM and discard any below the relevant context floor (never
   silently reduce).
3. **Qualify per role** with a bounded, deterministic corpus:
   - *generator*: a small code suite run through the Job Pod with **real
     tests** (F4-T0) — pass@k;
   - *judge*: the `eval/anchor` set scored with the `internal/judge` agreement
     math (F4-T4), plus reliability;
   - latency/VRAM footprint from the runs.
4. **Produce a reviewable routing table** (persona → model) written to config
   or surfaced for HITL approval; every application is logged. It *advises*
   routing and never overrides the frozen judge, deterministic verifiers,
   containment, or the HITL rules.
5. **Re-validate over time** — on model install/upgrade and periodically
   (the "promotion ratchet": a change must beat the incumbent, be reversible,
   and be logged).

Timing: an initial pass is a natural `athanor doctor` step; the recurring pass
is a Daydreaming action (AC power, low priority, yields to real work).

## Consequences

- The routing decision becomes evidence-based and hardware-aware instead of a
  hand-edited table, and it improves as models improve.
- The qualification corpus must be large and objective enough to be
  trustworthy: the original 8-case anchor produced a wrong 0.00, so the
  corpus needs the C0 expansion (objective code cases + a wider rating
  range) and likely more.
- It is measurement, not training: no weights change, no router bandit. It
  stays inside the F4 "no ML" boundary and the "rivers vs mountains" rule.

## Out of scope

- Any learned router / bandit / fine-tuning (future milestone).
- Automatic config mutation without review: the first version emits a
  proposed table for HITL approval.
