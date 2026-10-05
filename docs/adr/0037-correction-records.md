# ADR 0037 — CorrectionRecords (M6-T6)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §18
(§18.1–§18.4), §28.1; ROADMAP M6-T6; ADR-0035 (HITL queue).

## Context

ARCHITECTURE §18 makes a `CorrectionRecord` the unit of prescriptive
negative feedback: structured, project- or global-scoped, and injected into
future prompts (§18.3). The `corrections` table has existed since migration
0002, but it has no Go repository, and it lacks three §18.2 fields:
`artifact_id`, `scope`, and `user_feedback`. Nothing writes a correction
today, and §18.4's mandatory rejection form is unimplemented.

## Decision

### 1. Migration 0019 completes the §18.2 shape

`ALTER TABLE corrections ADD COLUMN` adds `artifact_id` (nullable FK),
`scope` (default `project`), and `user_feedback` (default `''`). As with
migration 0006, the value set for `scope` is enforced in Go because
`ADD COLUMN` cannot add a table-level CHECK.

### 2. `internal/corrections` owns capture and retrieval

- `Source` names the nine §18.1 sources. A source table maps machine sources
  to a default `scope`/`category`/`severity`/"derived rule" (e.g. a security
  scan is a `global`, `critical` security correction; a loop is a
  `performance` correction; a hallucinated path is a `documentation`
  correction).
- `Repo.Capture(CaptureInput)` validates the result against the §18.2 closed
  sets, applies source defaults, and — for the user-driven sources
  (`user_rejection`, `user_correction`) — requires the §18.4 form: category,
  severity, reason, desired behavior, and scope. A missing field is
  `ErrIncompleteFeedback`, and nothing is persisted.
- `Repo` also provides `Active` (severity-ordered, for injection),
  `ListByProject`, `SetStatus` (mute/promote), and `MarkApplied`. Every
  capture appends a `feedback` event (§28.1).

### 3. The engine reports its observable failure

`Engine.SetCorrectionSink` is a nil-safe seam: a phase failure is captured as
a `runtime_error` correction. The remaining §18.1 sources are exposed by the
same `Capture` API for the observers that see them; wiring test-failure,
evaluator-failure, and security-scan capture into the evaluation path is
follow-up work, and loop/hallucination detection are M7 alarms (§22.3). This
ADR records the capture layer and the mandatory form, not the injection path
(that is M6-T7).

### 4. API and CLI expose the mandatory form

`POST /projects/{id}/corrections` (the §18.4 form), `GET
/projects/{id}/corrections`, and `PATCH /corrections/{id}` (mute/promote) are
mirrored by `athanor reject ...` and `athanor corrections -project <id>`.

## Consequences

- Every rejection can yield a well-formed, scoped record, and the store
  rejects an incomplete user form rather than persisting a half-record.
- Correction capture is decoupled from injection: M6-T7 reads `Active` and
  assembles it into the prompt; M6-T9 proves the round trip.
- No new dependency; Gate G1 is unaffected.

## Implemented (M6-T6.2–T6.5)

Migration 0019; `internal/corrections` (source table, `Capture`, `Active`,
`ListByProject`, `SetStatus`, `MarkApplied`) with per-source tests; the
engine `CorrectionSink` seam (phase failure → `runtime_error`); the
`POST`/`GET /projects/{id}/corrections` and `PATCH /corrections/{id}` routes;
and `athanor reject` / `athanor corrections`. All nine §18.1 sources are
covered by `TestCaptureAllSourcesProduceRecords`, and the §18.4 form by
`TestCaptureRequiresFullUserForm`.
