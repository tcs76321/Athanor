# ADR 0068 — Synthesis must not discard a verified candidate

**Status:** Accepted · **Date:** 2026-10-15 · **Refs:**
ARCHITECTURE §9.3, §13.1 (Phase 5); ROADMAP §3 (G3);
[ADR-0066](0066-release-scope-soak.md); [ADR-0067](0067-technique-guidance-loop.md)

## Context

During the 0.0.0 validation soak (M8-T23 harness, hard corpus),
`code`-archetype jobs failed with `winner: none` **even though
`EvaluationRecord`s showed candidates that passed the real test command**. The
event trace for one job was:

```
verification where=candidate passed=false   (candidate 1)
verification where=candidate passed=true    (candidate 2)   ← a real pass
verification where=final     passed=false   ← synthesis rewrote a different candidate, broke it
comparison winner=none  → job failed
```

Root cause: `phaseSynthesize` refined `LatestForJob(..., KindProposal)` — the
**last** divergence proposal — rather than the candidate that passed evaluation.
The synthesis rewrite then produced code that failed the project's real tests,
so the comparison verifier rejected the final artifact and the job failed. A
candidate that had passed the deterministic verifier was discarded.

## Decision

**1. Synthesize from the passing candidate.** Synthesis selects the
highest-scoring candidate that passed deterministic verification (real tests, no
missing criteria, no security issues), falling back to the latest proposal only
when no candidate passed. Candidate choice is the same signal `phaseEvaluate`
already computed — no new inference.

**2. Never lose a verified candidate.** For the `code` archetype, the synthesized
final is verified before it is persisted; if it fails the deterministic verifier
while the source candidate passed, the candidate's bytes become the final
artifact. The fallback is audited as `synthesis_fallback_to_candidate`.

**3. Gate G3 is preserved.** The final artifact is still verified — the fallback
substitutes bytes that *also* passed the real tests, so it can only ever make
acceptance more honest, never weaker. The deterministic verifier remains the
load-bearing acceptance signal.

## Consequences

- Code jobs accept a candidate that passed the real tests instead of failing
  because the prose-synthesis rewrite degraded it.
- One extra deterministic verification runs per code job in `synthesizing`
  (the run is repeated by the comparison phase; the redundancy is cheap relative
  to the lost acceptance it prevents).
- The pod-tool call counts asserted by `internal/engine` tests rise from 8 to 10
  for a code job (3 candidates + synthesis fallback + comparison final).

## Alternatives rejected

- **Skip synthesis entirely for code and promote the candidate.** Discards the
  documentation/change-summary synthesis the phase exists to add.
- **Accept the synthesized final without re-verifying.** Would violate G3 (no
  artifact accepted without passing deterministic evaluation).
- **Keep refining the latest proposal.** The defect this ADR fixes.
