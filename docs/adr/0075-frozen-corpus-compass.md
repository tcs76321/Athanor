# ADR 0075 — The frozen-corpus soak is a permanent design compass

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
[ADR-0066](0066-release-scope-soak.md) (coverage-based soak),
[ADR-0061](0061-harder-benchmark.md) (harder corpus),
[ADR-0073](0073-soak-validation-harness.md) (soak harness),
[ADR-0076](0076-continuous-evidence.md) (real-use evidence); ROADMAP §3 (G7/G8).

## Context

The soak began as a release-scope checkpoint ([ADR-0066](0066-release-scope-soak.md))
and now also carries conformance and benchmark readouts
([ADR-0073](0073-soak-validation-harness.md)). We need it to do a different,
longer-lived job: **track whether each Athanor version moves the system forward
or degenerates**, across changes to the engine, policies, verifiers, context
assembly, scheduler, and routing.

Three constraints shape that:

- A model-driven, multi-hour run **cannot live in CI**.
- A **formal release gate** would block tags on a human-timed run and turn a
  design instrument into process overhead.
- A corpus that changes between runs **cannot be compared across versions**.

## Decision

1. **Not CI/CD, not a formal gate.** The soak is a **design compass** and a
   **pre-tag double-check**, documented (AGENTS + testing docs) as the
   controlled study that tracks progress over time. Running it is a discipline,
   not a pipeline stage.
2. **A frozen corpus v1, pinned from 0.0.0 until 1.0.0.** It is content-hashed,
   **never edited**, and large and representative enough to carry the period:
   every archetype (code/document/text/data/adversarial), E/M/H tiers, and
   enough hard tasks that a good model does not saturate it. A **held-out
   slice** is never used for tuning. New tasks go into a **v2** corpus; v1 stays
   comparable forever.
3. **Two tiers.**
   - **Smoke**: a small fixed subset, run on any change to the named engine
     surface (`internal/engine`, `internal/policy`, `internal/verify`,
     `internal/mce`, `internal/prompt`, `internal/scheduler`, and the LLM
     client), with a short delta note.
   - **Full**: the whole frozen corpus, before a tag.
4. **Store raw outputs, not just aggregates.** Artifacts, events, verdicts, and
   costs are retained per version so a future, better grader can re-score old
   runs; otherwise the history is only as good as the grader of its era.
5. **Soaks vary Athanor, not the world.** Arms, per-role models, gates,
   escalation, and routing are the independent variables; the frozen corpus is
   the constant. It is a controlled study of the *system*.
6. **Two-sided acceptance.** The frozen corpus must **not regress**, and real-work
   evidence ([ADR-0076](0076-continuous-evidence.md)) must **improve**. Neither
   number alone is a claim.

## Consequences

- The corpus must be **expanded before the 0.0.0 freeze**; freezing 15 tasks
  would be too small and too easy to saturate.
- Retention is a real cost (raw artifacts per version); plan compaction that
  keeps the decision-relevant signal.
- The "run it when you touch the engine" rule is documented discipline; a
  reviewer can ask for the smoke delta in a PR description.
- No CI wiring, no tag-blocking automation.

## Alternatives rejected

- **CI/automated soak.** Needs local models, podman, and hours of wall clock.
- **A formal release gate.** Blocks the tag on a human-timed run; the design
  value is in the trend, not a binary pass.
- **A mutable corpus.** Destroys cross-version comparability, which is the point.
- **Aggregates only.** Prevents re-scoring with improved verifiers and judges.
