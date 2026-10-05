# ADR 0039 — Local web UI (M6-T8)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §11, §20.3,
§20.4, §21.8, §27; ROADMAP M6-T8a/b/c; ADR-0035 (HITL), ADR-0037
(CorrectionRecords), ADR-0038 (feedback injection).

## Context

M6 is otherwise headless: jobs, approvals, corrections, and watch data exist
in SQLite and over JSON routes, but §27 specifies a UI (Dashboard, Watch,
Approvals, Projects, Artifacts, Corrections). M6-T8a/b/c call for a dashboard
that reflects a running job's phase within ~1s via SSE, approvals that
resume the right job, project/artifact/correction CRUD with a mandatory
rejection form and version diffs, and a watch view with live phase
transitions, a token stream, and a queue that does not interrupt mid-token.

## Decision

### 1. `internal/ui`, stdlib only, on the loopback listener

The UI is server-rendered `html/template` plus Server-Sent Events, mounted at
`/ui` on the daemon's existing loopback server (§21.8). No web framework, no
new dependency. There is no authentication because the listener is
loopback-only; remote access is SSH port forwarding (§21.8).

### 2. SSE for live updates

- `/ui/events` streams every new event; the dashboard script updates a job's
  phase cell from `transition` rows.
- `/ui/jobs/{id}/stream` multiplexes streamed model tokens (`event: token`)
  and that job's new events (`event: event`).

Both poll the append-only event log every 500 ms and advance a `lastID`
watermark, so updates land well within the ~1s bar without a second event
bus. The token stream is pushed through the `Hub`.

### 3. Streaming is a nil-safe engine seam

`llm.Client.Stream` speaks Ollama's newline-delimited streaming API;
`engine.TokenSink` receives incremental chunks. When no sink is wired the
engine uses the non-streaming `Chat` (the pre-T8 path), so existing callers
and fakes are unchanged. The UI's `Hub` is the sink: it fans chunks to
watch-view subscribers and drops them for a slow subscriber rather than
blocking the engine.

### 4. The interruption queue is real (§20.4)

Migration 0020 adds `interruption_notes`; `internal/interruptions` persists
them. The engine drains a job's pending notes at context assembly (prompt
`InterruptionNotes`, `§11.2 §15` / tier 7), marks them injected, and audits
`interruption_injected`. A note therefore rides the next prompt — never an
in-progress generation.

### 5. Mutations go through the existing stores

Approve/reject/defer uses the M6-T4 `hitl.Service`; the rejection form uses
`corrections.Capture` (the §18.4 mandatory fields are enforced server-side);
mute/edit/promote uses `SetStatus`/`Update`; the interruption form uses
`interruptions.Add`. Job controls use the job repository: pause/resume via the
`paused` state, cancel via `cancelled`, retry by creating and enqueuing a new
job for the same task. Artifact diffs are an in-UI LCS line diff between a
version and the one it superseded.

## Consequences

- An operator can watch a job, approve an escalation, reject an artifact with
  the structured form, and steer a run without leaving the browser; every
  page is a thin view over the same append-only state the CLI uses.
- Live token output required real Ollama streaming, which is now available to
  any future caller, not just the UI.
- The UI adds no dependency and no network listener beyond the existing
  loopback one; Gate G1 is unaffected (no tool execution, no outbound HTTP).
- Full view parity with §27 (Statistics, Memory Browser, DAG visualizer) stays
  backlog; the diff view is a basic line diff.

## Implemented (M6-T8.2–T8.5)

`llm.Client.Stream`; the engine `TokenSink`/`InterruptionStore` seams;
migration 0020 + `internal/interruptions`; `internal/ui` with Dashboard,
Projects (+ artifact diffs), Corrections (+ §18.4 form, mute/edit/promote),
and Watch (+ SSE tokens/events, interruption notes, pause/resume/cancel/
retry); and boot wiring that mounts it on the loopback server.
