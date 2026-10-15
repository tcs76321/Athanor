# ADR 0066 — 0.0.0 release scope and the soak re-specification

**Status:** Accepted · **Date:** 2026-10-15 · **Refs:**
ROADMAP §3 (gates G7/G8); [m7-gate-g7.md](../m7-gate-g7.md);
[soak-m7.md](../soak-m7.md); [m8-process-matrix.md](../m8-process-matrix.md);
[ADR-0063](0063-f4-closeout.md); [ADR-0061](0061-harder-benchmark.md);
[ADR-0067](0067-technique-guidance-loop.md)

## Context

M0–M6 are complete, the F-tracks (F1–F5) have landed, M7 is code-complete, and
M8 is near-close. Two gates remain, and both are **human checkpoints** rather
than missing code:

- **Gate G7** — "System survives sleep, power loss, and 24h soak with no state
  loss." Its remaining arms are the soak, the fresh-install demo (<15 min), and
  the §31.3 audit sign-off ([m7-gate-g7.md](../m7-gate-g7.md)).
- **Gate G8** — end-to-end validation on the hard, deterministic-first corpus; a
  single combined run (endurance + recovery + power + real-world-internal
  workflows) that loses no state; and cognitive-operation selection learned from
  grounded outcomes with a novelty reset.

[ADR-0063](0063-f4-closeout.md) already fused G7 and G8 into one combined
validation run: G7's remaining human arms are to be satisfied as evidence from
that run, so the two gates close together.

The literal G7 text names a **24-hour** soak. The M8-T23 harness
(`m3-t7-probe soak`) is a single long-lived daemon that is resumable
(append-only `metrics.jsonl`), rolling (a `report.md` after every pass),
early-stoppable (SIGINT finalizes), and can inject one `kill -9`. Its endurance
value is concentrated in a few properties — steady-state plateau, crash
recovery, and the time-triggered subsystems firing — not in the wall-clock
number itself. Waiting a fixed 24 hours is not proportionate to the residual
risk once the time-triggered arms are covered deliberately.

## Decision

**1. 0.0.0 closes G7 and G8 together**, from one combined validation run over
the hard corpus (`eval/bench/`), as ADR-0063 anticipated.

**2. The soak acceptance is re-specified by coverage, not by wall-clock.** A run
satisfies Gate G7's soak arm when it demonstrates all of:

- **No orphan Job Pods** at the end of the run and after the `kill -9`
  (`podman ps -a` shows zero `athanor-job-*`).
- **No unrecovered crash**: the process killed mid-run recovers from its last
  committed state on restart (§23.6), or the failure is captured with a reason
  in the event log.
- **Bounded growth**: RSS, **goroutine count**, SQLite/WAL size, and disk do not
  grow monotonically — they plateau. The sampler records RSS, DB size, free
  disk, and (new) goroutine count.
- **One human sleep/wake cycle** pauses and resumes the run per §24.
- **The time-triggered subsystems fire** within the window: the scheduled
  backup, HITL TTL expiry, and the alarm poll. Because a run that ends before
  `backup.schedule` (`0 3 * * *`) will not reach the production trigger, the
  soak config **compresses** these schedules so they fire during the run; the
  production `0 3 * * *` spec remains pinned by the `internal/backup` cron unit
  test.

The run length is a **deadline**, not a duration: `m3-t7-probe soak -until
<deadline>` runs until the operator's chosen wall-clock deadline (e.g. a return
time), persisting the resolved deadline so a resume does not slide it.

**3. The G8 learning arm ships as advisory technique guidance**
([ADR-0067](0067-technique-guidance-loop.md)); it is not an operation-narrowing
mechanism.

**4. Out of scope for 0.0.0** (tracked post-0.0.0): statistical power for the
guidance claim; deterministic verifiers for `data`/`media`; model
onboarding/qualification ([ADR-0049](0049-model-onboarding-qualification.md));
the brainstorm corpus task; adversarial self-critique and tree search
(M8-T12, deferred with rationale); Browser Mode; the cloud credential broker;
the skills runtime and `skill_refinement`; Exploration Paths; the full
Statistics/Memory-Browser views.

## Consequences

- The 24-hour figure is retired in favour of a coverage checklist. The residual
  risk — a run that does not cross a full day — is mitigated by exercising the
  time-triggered arms deliberately (compressed schedules + their unit tests)
  rather than by waiting.
- 0.0.0 can be shipped on the operator's schedule rather than on a fixed
  24-hour clock.
- Soak evidence lands under a gitignored results directory; a dated summary is
  committed under `docs/` and the checklist in
  [m7-gate-g7.md](../m7-gate-g7.md) is signed off.
- `ROADMAP.md`, the README status, and the soak runbook are updated to match
  this decision.

## Alternatives rejected

- **Keep the fixed 24-hour run.** It buys a full-day-crossing run at the cost of
  a day of wall-clock, and none of the endurance properties depend on the
  exact figure once the time-triggered arms are covered.
- **Drop endurance validation entirely.** That discards Gate G7; the soak is the
  only end-to-end test of steady-state behaviour under sustained load.
- **Fully automate the time arms (clock injection) and remove the run.** The
  sleep/wake cycle and real-load steady state are still human/hardware
  checkpoints; a hybrid is honest.
