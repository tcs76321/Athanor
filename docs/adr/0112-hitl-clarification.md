# ADR 0112 — Mid-job clarification as a HITL request

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §20; ADR-0035 (HITL queue), ADR-0109 (job notebook);
`internal/engine`, `internal/hitl`, `internal/notebook`, `cmd/athanor`

## Context

Athanor's HITL queue is a set of **approval** gates: external/irreversible
actions, task escalations, insights, and network access. The project interview
and onboarding gather context *before* work starts. But ambiguity also appears
*mid-job*: the model discovers a missing fact it cannot infer, and the current
loop has no way to ask. It either invents an answer, guesses, or fails and
escalates through the failure policy.

The interactive-agent pattern that makes long work effective is exactly this:
the agent stops and asks a specific question, gets an answer, and continues
with the answer in working memory. Athanor has a durable pause state
(`awaiting_approval`) and a notebook; the missing piece is a first-class
question/answer request.

## Decision

Add a **clarification request** to the HITL queue, initiated by the model and
answered by the operator.

- **Marker.** Planning and reflection prompts tell the model: if a fact only
  the operator can supply blocks a correct plan, end with one line
  `QUESTION: <one sentence>` and do not invent it.
- **Park.** The engine detects the marker, writes a `question` notebook entry,
  and calls the `Clarifier` seam. The concrete adapter files a
  `clarification` HITL request via `Service.Await`, which moves the job to
  `awaiting_approval` and records `awaiting_from` (planning or reflecting).
  The engine returns `ErrPaused`, so the phase is not failed.
- **Answer before resume.** A clarification answer is a **pre-approver**: the
  HITL service runs it *before* `applyOutcome` resumes/enqueues the job. It
  appends an `answer` entry to the notebook. This ordering is the whole point;
  a normal approver would race the engine's next prompt read.
- **Resume.** The job re-enters the same phase. The notebook contains the
  question and answer, so the next plan/reflection sees both.
- **Expiry.** An unanswered clarification denies by default like every other
  HITL request (the job fails); this is the existing §20 policy, not a new one.
- **No clarifier, no park.** A daemon without the seam ignores the marker and
  continues, preserving the pre-ADR behavior for tests and minimal configs.

## Consequences

- The model can ask instead of inventing. The question and answer are durable,
  auditable, and visible in the approvals page.
- The loop remains contained: a question is a request, not a tool call; it
  cannot execute anything.
- A poor planner may ask too often. The prompt limits the marker to blocking
  facts, and the existing HITL TTL/deny path bounds the cost; a future
  calibration can count question rate per task class.
- The pattern is the conversational counterpart to the notebook: the notebook
  carries what the job knows; the clarification carries what only the operator
  knows.

## Alternatives rejected

- **Add a new job state (`awaiting_input`).** `awaiting_approval` already has
  the durable pause/resume, `awaiting_from`, TTL, and UI/CLI plumbing. A new
  state would duplicate it and expand the state machine for no gain.
- **Let the model ask in free text and hope the operator sees it.** The
  question would not pause, would not be answerable, and would not be in the
  notebook at the right time.
- **Answer as an interruption note after resume.** That races the next prompt
  and depends on timing; the pre-approver ordering is deterministic.
