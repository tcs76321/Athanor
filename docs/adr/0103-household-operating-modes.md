# ADR 0103 — Household operating modes, not a resource free-for-all

**Status:** Accepted · **Date:** 2026-11-05 · **Refs:** ARCHITECTURE §17,
§24, §27; [ADR-0051](0051-power-supervisor.md),
[ADR-0072](0072-single-slot-execution.md),
[ADR-0096](0096-capacity-aware-concurrency.md),
[ADR-0102](0102-separate-host-and-runtime-memory-budgets.md).

## Context

The power policy answers "may Athanor work right now?" from AC/battery, sleep,
and the kill switch. It does not answer the household question: *is the person
using this computer, and did they ask Athanor to stay out of the way?* Without
that distinction the only options were "running" and "frozen", so unattended
overnight work and active daytime use looked the same to the scheduler.

The user-facing modes also have to be honest about resources. A mode is an
expectation about availability, never permission to saturate the machine.
Stopped must actually stop work and release memory, not merely hide a button.

## Decision

1. **Four modes, one controller.** `internal/operator` owns the persisted mode:
   **Deep** (unattended, no feedback expected), **Full** (present, work and
   daydream when nothing better is queued), **Lite** (user work only, CPU
   capped, no daydreaming), and **Stopped** (no work; active jobs pause, pods
   stop, configured models unload; explicit resume required). Auto selects a
   mode from presence, and a temporary "use the computer" hold (30m/1h) expires
   back to Auto.
2. **The gate sits next to the power gate.** The engine consults the operator
   gate at every phase boundary and refuses to enqueue new work while stopped.
   A stopped Athanor parks active jobs in `paused` (resumable), never
   `cancelled` or `failed`; `ResumeAll` re-drives paused jobs and re-enqueues
   held queued jobs.
3. **Daydreaming is Full-only.** Deep is committed work, not idle filler; Lite
   is user-started work only. The daydream loop checks the mode on every tick.
4. **Lite halves the Job Pod CPU ceiling.** Concurrency is one and the pod's
   CPU limit is halved, so user-started work cannot saturate the machine.
   Inference is not throttled at the Ollama level (the client has no portable
   per-request CPU control); single-slot inference and one job are the
   available levers.
5. **Stopped does not stop Ollama.** It unloads the configured persona models
   and leaves the shared service running; other local software may depend on
   it, and Athanor must be a good housemate.
6. **Auto never claims Deep without evidence.** Presence comes from an optional
   host-written heartbeat (`ATHANOR_PRESENCE_FILE`): `present`, `idle_seconds`,
   `load`. With no heartbeat Auto defaults to Full. Deep requires a long
   absence; a present machine gets Full when light and Lite when busy.
7. **The dashboard is the control surface.** Deep/Full/Lite/Auto buttons, a red
   Stop, Resume when stopped, 30m/1h holds, the effective mode, the reason, and
   the current concurrency/CPU/daydream allowances are visible together.

## Consequences

- Household mode is persisted in `system_state` under `operator:state` and
  survives a Core restart. Corruption is an error, not a silent mode change.
- The engine ceiling composes the operator mode with the power/config ceiling,
  so a lower layer can still reduce concurrency; Stopped is zero.
- A stop is observable and resumable: jobs keep their checkpoint, and the
  event log records `job_paused` with `reason: operator`.
- Presence sensing is deliberately a file seam. macOS/Windows helpers or a
  launchd/systemd agent can write the heartbeat without granting Athanor
  window-title, keystroke, or content access.
- The kill switch and the power policy remain independent safety signals. A
  frozen system, or a laptop on battery, pauses work regardless of mode.
