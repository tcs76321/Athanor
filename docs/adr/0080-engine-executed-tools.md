# ADR 0080 — Tools are engine-executed; the model does not call them

**Status:** Accepted · **Date:** 2026-10-24 · **Refs:**
ARCHITECTURE §10.1 (context swap), §11.2 (prompt assembly), §25 (tool
interface); [ADR-0009](0009-engine-pod-wiring.md) (engine ↔ pod dispatch),
[ADR-0019](0019-gateway-tools.md) (engine-driven research sub-step),
[ADR-0023](0023-context-assembly-priority.md) (tiers),
[ADR-0024](0024-engine-pod-dispatch.md) (pod dispatch),
[ADR-0079](0079-no-skills-runtime.md) (closed internal surface);
[bench-v1 findings](../bench-v1-findings.md) (`text-e-bio` tool transcript).

## Context

The prompt's containment section said: *"You may call ONLY the tools listed
below, through the tool interface"* and listed the per-job envelope
(`execute_code`, `run_tests`, `lint`, `fetch_url`, `search_web`,
`context_swap`, `query_memory`). The engine, however, has no tool-call parser
and no model-driven loop: it runs code/tests/lint and research fetches as
deterministic sub-steps on a candidate, and it evicts/loads context chunks via
the §10.5 ladder. The model cannot invoke any of them.

That mismatch is not cosmetic. The v1 benchmark audit found a text artifact that
was a **tool-call transcript** (`text-e-bio`: "I'll explore the project…" plus a
`<tool_call>` block) rather than the requested bio. A prompt that promises a
tool interface the runtime does not implement invites exactly that failure.

A real model-driven loop is a different system: it needs a tool-call protocol
and parser, per-call and per-job budgets, loop detection, new audit rows,
containment review, and a prompt-injection defence that assumes the model can
act on fetched text. None of that exists today, and there is no evidence yet
that it improves hard-task outcomes.

## Decision

1. **Athanor is engine-driven in 0.0.0.** The Core decides when to run an
   operation on a candidate; the model produces artifacts only.
2. **The prompt says so explicitly.** The operations section is labelled
   "ENGINE-EXECUTED OPERATIONS (the Core may run these; you cannot invoke
   them)", and the synthesis/refine policy requires artifact-only output with
   no tool-call syntax or planning narration.
3. **The Dormant Index does not invite a model tool call.** It says the Core
   keeps one chunk active and may load a dormant chunk when the budget requires
   it.
4. **The closed tool envelope remains meaningful** — it bounds what the engine
   may run for a job, and it is still proven by Gate G2. It is not a model
   protocol.
5. **A model-driven loop is deferred, not rejected forever.** The revisit
   trigger is evidence: if a repo-backed working set (the 0.0.x capability work)
   and the frozen benchmark show the engine-driven path is the limiting factor,
   open a new ADR for a bounded loop. Until then, no parser, no loop, no new
   tool surface.

## Consequences

- The prompt no longer promises a capability the runtime lacks. A tool-call
  transcript is a model error the engine can reject rather than a plausible
  response to a false instruction.
- Artifact-only synthesis is enforced by prompt policy; the benchmark's
  artifact-only rule and the engine now agree.
- The engine-driven design remains cheaper and easier to contain: one
  deterministic sub-step at a time, bounded by phase budgets and the per-job
  envelope.
- Open-ended repository exploration remains weaker than a tool loop. That is
  the known cost, addressed first by the staged repository working set
  (0.0.x), not by adding a loop before the evidence supports it.

## Alternatives rejected

- **Implement the loop now.** Large new surface (protocol, budgets, loop alarm,
  injection, gates) with no measured benefit and a freeze imminent.
- **Leave the prompt as-is.** It explicitly invites the failure mode the v1
  audit found.
- **Remove the envelope from the prompt entirely.** Then the prompt cannot
  explain what the engine will do with the artifact, and the model may assume
  capabilities that do not exist in the other direction.
