# ADR 0053 — Alarm system

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §22.3
(categories, levels), §22.1 (freeze), §28.1 (`alarms` events); ROADMAP M7-T3

## Context

Only the kill switch existed; the nine §22.3 alarm categories had no model, no
persistence, no triggers, and no escalation. M7-T3 must implement the
category/level system, wire detectors to their triggers, add a triggered test
per category, and make a `critical` alarm freeze the system.

## Decision

- **Model + persistence** (`internal/alarms/alarms.go`, migration 0023):
  closed `Category` and `Level` sets, `DefaultLevel(category)` per §22.3, and a
  `Service` that raises/dedups/resolves alarms, audits `alarm_raised`, and
  freezes through the injected `Freezer` on `critical`. Dedup is by (category,
  message, job, project) while active, so a persistent condition does not spam
  the table.
- **Pure detection** (`detect.go`): `Detect(Snapshot, Thresholds, now)` is a
  pure, deterministic function covering all nine categories — quality
  rejection rate over the last window, stuck jobs, repeated identical tool
  calls (loop), per-job token budget, memory/disk pressure (resource),
  airlock/network rejects (security), and the `hallucinated_path` /
  `self_modification_attempt` / `drift_attempt` audit events. Every category
  is a table test.
- **Monitor** (`monitor.go`): a poll loop over a `Loader`. The production
  `StoreLoader` populates the snapshot from SQLite (outcomes, non-terminal
  jobs + last event, `actions` loop counts, token spend, security events, and
  event flags). Resource pressure is left at 0 until a sampler is added — the
  field is present so it needs no API change.
- **Surfaces**: `GET /alarms` + `POST /alarms/{id}/resolve` and
  `athanor alarms [-resolve <id>]`. The daemon wires the monitor and the
  alarm service at boot; a critical alarm closes the same loop as
  `athanor freeze`.

## Consequences

- Critical alarms (security, self_modification, drift) freeze the system
  through the existing kill switch, so there is one freeze authority (§22.1).
- `self_modification` and `drift` are detected from audit events; the guards
  that emit them today only exist conceptually (static prompts are compiled
  in), so these categories are raise-ready and tested but will fire only once
  the corresponding guards emit their events. This scope limit is explicit.
- Thresholds are constants in `internal/alarms`; a §29 `alarms:` block can
  expose them later without changing `Detect`.
