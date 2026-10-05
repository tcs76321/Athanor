# Athanor — UI/UX Design Note

**Status:** design note / proposal · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §20, §22, §27; ADR-0039 (local web UI); `internal/ui`

The UI is Athanor's face for a machine that runs unattended. This note records
what ships today, the intended views (§27), the gaps, and a design direction
that respects the project's constraints (loopback-only, no new dependencies,
`html/template` + SSE, local-first).

## 1. What ships today (`internal/ui`)

| Route | View | Notes |
|---|---|---|
| `GET /ui` | **Dashboard** | active/terminal jobs, pending approvals, freeze state; live phase via `/ui/events` (SSE) |
| `GET /ui/projects`, `/ui/projects/{id}` | **Projects** | artifact history with an LCS diff against the superseded version |
| `GET /ui/corrections`, `POST` | **Corrections** | the mandatory §18.4 rejection form; mute/edit/promote |
| `GET /ui/statistics` | **Statistics** | read-only; strategy insights with evidence |
| `GET /ui/jobs/{id}` (+`/stream`, `/note`, `/control`) | **Watch** | streamed tokens + events, interruption notes, pause/resume/cancel/retry |
| `POST /ui/approvals/{id}` | **Approvals** | approve/reject/defer inline on the dashboard |

Mounted at `/ui` on the loopback listener (ADR-0039); stdlib only.

## 2. Intended vs shipped (ARCHITECTURE §27)

| §27 view | Status | Gap |
|---|---|---|
| Dashboard | ✅ shipped | power/daydream state, resource usage, alarms not surfaced |
| Watch | ✅ shipped | candidate diff view, evaluation-score timeline are thin |
| Approvals | ◑ inline | no dedicated queue depth/badge, no risk-level grouping |
| Projects | ✅ shipped | no task-DAG visualizer |
| Artifacts | ◑ in Projects | no standalone browser, no open-in-workspace, no export button |
| Memory Browser | ❌ | Dormant Index viewer, compaction layers, retrieval test tool |
| Network Log | ❌ | allowed/denied/rate-limited requests; reader-mode status |
| Security View | ❌ | quarantined files, scan failures, injection detections, alarm history, kill switch |
| Statistics | ◑ read-only | approval/rejection rates, token usage per persona/project, duration, budgets |
| Settings | ❌ | personas, floors, power, allowlist, backup, budgets |
| Morning Digest | ❌ | async overnight summary |
| First-run onboarding | ❌ | `athanor doctor` + create first project are CLI-only |

## 3. Design principles (for *this* machine)

1. **Calm by default, loud when it matters.** A 24/7 furnace should look idle
   and healthy at a glance, and demand attention only for approvals, alarms, and
   failures. No vanity charts.
2. **The Watch is the product.** For an autonomous agent, watching work unfold —
   phases, tokens, tool calls, tests, the judge's verdict — is the core
   experience, not a sidebar.
3. **HITL is a first-class queue, not a modal.** Approvals need low latency,
   clear risk, expiry visibility, and a decision record. (§20)
4. **Safety is visible.** Quarantines, gate status, and the kill switch should
   be as prominent as activity — users should *see* containment working.
5. **Local, honest, and quiet.** Loopback-only, no telemetry, no external
   fonts/CDNs (offline must work). Prefer server-rendered HTML + SSE over a JS
   framework; the project's stack discipline applies to the front end too.
6. **Progressive disclosure.** Dashboard → project → job → artifact → raw event.
   Never show the raw event log first; always allow drilling to it.

## 4. Information architecture

A single left rail (or top nav), ordered by frequency of need:

```
Dashboard · Approvals (badge) · Projects · Memory · Network · Security · Statistics · Settings
```

Watch is reached from Dashboard/Projects, not the rail. A persistent **status
strip** shows freeze state, power state, active job count, and pending
approvals, on every screen.

## 5. Key flows

**First run.** Replace the CLI-only path with a guided flow: environment check
(`doctor` results), persona/model mapping with pull buttons, then *create the
first project* (name, goal, archetype, criteria) — **never a chat box** (§30.3).
End state: a project queued or running, and a clear "what happens next".

**Submit a goal.** A form with archetype-specific fields (§6.2: language/repo/
test command for `code`; structure/citation for `document`). Show the resolved
budget and the compute plan (once F4 lands, show "N candidates / verifier-first"
so the user understands the cost).

**Watch a job.** Phase tree with the current phase highlighted; live token
stream; tool-call log with input/output; test output; candidate cards with
evaluation scores; the comparison verdict with reasons. Controls: pause / stop /
retry / note. An interruption note is queued to the next safe point (§20.4) and
the UI must say so ("note queued — applied at next phase").

**Approve/reject.** The queue shows project, task, job, risk, reason, requested
action, and **expiry countdown**. Approve/reject/modify/defer; every decision is
recorded. A rejected artifact opens the §18.4 structured form inline.

**Review an artifact.** Versioned list with status badges, a diff against the
prior version, the `EvaluationRecord`, the Git commit on the agent branch, and
**Open in workspace** / **Export**.

**Correct.** The structured rejection form (category, severity, reason, desired
behavior, scope) with immediate feedback that a `CorrectionRecord` was created
and will be injected into future prompts.

**Morning digest.** After an idle/overnight period: completed DAGs, drafts and
accepted artifacts, failures, pending approvals, daydream output, token usage —
one scrollable page, links into everything. (§27.2)

## 6. Screen notes (prioritized)

- **Dashboard:** jobs grouped Active / Needs attention / Done; approvals card
  with count + oldest age; alarms ribbon; freeze control with confirm. Live
  phase updates via SSE.
- **Watch:** the deepest view; keep it fast and text-first. Candidate cards
  should show side-by-side diffs and the judge's `missing_criteria`.
- **Approvals:** the dedicated queue, sorted by risk then age; expiry visible.
- **Security:** quarantined files with the deciding scanner's reason, scan
  failures, injection detections; kill switch and sandbox status; a link to the
  Gate status. This view is the trust surface.
- **Memory:** Dormant Index viewer (chunk id, path, line range, 1-line summary),
  compaction layers with their Temp-0 provenance, and a **retrieval test tool**
  (query memory → see scored hits) — this is a debugging *and* trust feature.
- **Network:** allowed/denied/rate-limited fetches, reader-mode outcome,
  truncated responses. Default-deny should be legible, not scary.
- **Statistics:** approval/rejection rate and categories, token/cost by
  persona/project, duration, budget usage, strategy win rates + active insights
  with inline evidence. Read-only, honest about cohort sizes.
- **Settings:** personas/models, context floors, power policy, network allowlist,
  backup, budgets, workspace paths — each with the current effective value and
  where it came from (config file vs default).

## 7. Constraints and mechanics

- **Stack:** `html/template` + SSE + a little progressive-enhancement JS; no new
  dependencies (AGENTS/deps policy). System fonts or self-hosted assets only.
- **Bindings:** loopback only (§21.8); remote access via SSH port forwarding.
- **State:** every mutating action is a normal HTML form POST (works with JS
  off); SSE enhances.
- **Accessibility:** keyboard-first, focus-visible, semantic headings, color not
  the sole signal (status has text + color), reduced-motion respected.
- **Destructive actions** (freeze, cancel, delete) confirm and state their
  reversibility. HITL expiry is always shown so silence is never ambiguous.

## 8. Phasing

1. **Polish the shipped views** (Dashboard, Watch, Approvals, Projects): state
   strip, approvals badge/expiry, candidate diffs, alarms.
2. **Trust surfaces:** Security view + Network log (visibility into containment).
3. **Memory Browser + retrieval test tool** (makes the MCE legible and
   debuggable).
4. **Statistics depth + Morning Digest.**
5. **First-run onboarding** (align with M7 `doctor` + packaging).

## 9. Open questions

- Should the digest be a screen, an email/notification, or both? (Local-first
  suggests a screen by default; notifications are opt-in and HITL-scoped.)
- How much of the raw `EventLog` should be exposed vs a curated timeline? (Raw
  log is a power-user tool; default to curated.)
- Dark/light theming, and whether to persist UI preferences in
  `system_state` or per-project.
