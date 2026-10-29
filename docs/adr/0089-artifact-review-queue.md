# ADR 0089 — Accepted artifacts have a provenance-bearing review queue

**Status:** Accepted · **Date:** 2026-10-29 · **Refs:**
`docs/first-use.md` F4; [ADR-0076](0076-continuous-evidence.md) (continuous
evidence), [ADR-0088](0088-project-interview-plan-gate.md) (plan gate);
`docs/evidence-pipeline.md` §4 (human eval).

## Context

The 0.0.0 first-use loop needs a human signal on accepted artifacts: what was
built, how to run it, what is known-broken, and what it cost. The existing
accept/reject and correction paths capture *negative* feedback, and the judge is
advisory. There was no positive, provenance-bearing human review, and no queue
that asks the operator to review accepted work.

## Decision

1. **One `artifact_reviews` row per review** (migration 0030): artifact, job,
   project, reviewer, score `[0,1]`, accepted flag, blinded flag, rubric map,
   note, provenance map, timestamp.
2. **The queue is a query, not a table.** Accepted artifacts with no review,
   newest first. The API returns a blinded summary (`id`, `kind`, `project_id`,
   `created_at`) — no model name, no artifact content — so the rating is not
   anchored to the generator.
3. **API and UI share the store.** `POST /artifacts/{id}/review`,
   `GET /artifacts/{id}/reviews`, `GET /reviews`, `GET /reviews/queue`; the
   artifact page renders the form and prior reviews.
4. **The review is not an acceptance decision.** It is a label for later
   analysis; accepting/rejecting an artifact stays the engine/compare path. The
   review cannot mutate the artifact.
5. **Provenance is stored, not inferred.** The API records kind/version plus the
   artifact/job/project ids; a future reducer can join it without trusting a
   free-text note.

## Consequences

- The review queue is small and deterministic; it does not need a scheduler or
  a model call.
- The blinded payload is only as blind as the artifact id: a UI that shows the
  artifact content can reveal the generator's style. The API contract is
  blinded; a future UI should sample and hide model metadata.
- A review does not block or change work; it is advisory evidence, consistent
  with the anti-self-grading rule.
- The queue only includes `accepted` artifacts. Rejected artifacts are already
  captured as corrections.

## Alternatives rejected

- **Reuse `CorrectionRecord` for positive ratings.** Corrections are
  prescriptive negative feedback; mixing ratings into them would corrupt the
  prompt-injection ranking.
- **Store the review on the artifact row.** Artifacts are immutable acceptance
  records; reviews are append-only human evidence with their own provenance.
- **Model-generated review summaries.** The point is a human label; a generated
  summary would be another advisory judge.
