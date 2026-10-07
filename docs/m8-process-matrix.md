# M8 process & recovery matrix

**Scope:** the failure/process scenarios 0.0.0 must survive, each with the test
that proves it, plus the deliberately-human checkpoints. This is the evidence
set for Gate G7's "survives sleep, power loss, and 24h soak with no state loss"
and for Gate G8's combined run. Maintained by M8-T24.

Status legend: **auto** = enforced by `make check` / CI; **opt-in** = gated by
`ATHANOR_RUN_INTEGRATION=1`; **human** = a supervised checkpoint.

| # | Scenario | Proof | Status |
|---|---|---|---|
| 1 | Crash mid-`diverging` → resume from checkpoint | `internal/engine/recovery_test.go::TestRecoverResumesJob_MidDiverging` | auto |
| 2 | Crash mid-`evaluating` → resume | `…::TestRecoverResumesJob_MidEvaluating` | auto |
| 3 | Crash mid-`synthesizing` → resume | `…::TestRecoverResumesMidFlightJob` | auto |
| 4 | Artifact written before the state transition → no loss/dup | `…::TestRecoverResumesJob_ArtifactWrittenBeforeTransition` | auto |
| 5 | Job-level CAS resume from last committed state | `internal/job/recovery_test.go::TestCrashRecoveryResumesFromLastCommittedState` | auto |
| 6 | Migrations forward-only, idempotent, backup-before-migrate | `internal/store/*_migration_test.go`, `store_test.go` | auto |
| 7 | Backup → prune → restore drill | `internal/backup/backup_test.go::TestSnapshotPruneRestore` | auto |
| 8 | Kill switch freeze survives restart | `internal/control/killswitch_test.go::TestFrozenSurvivesRestart` | auto |
| 9 | Freeze/unfreeze HTTP contract (reason required) | `internal/server/freeze_test.go` | auto |
| 10 | HITL request expires → denied by default | `internal/hitl/hitl_test.go::TestExpireOverdue`, `::TestServiceExpiryDeniesByDefault` | auto |
| 11 | HITL approve resumes / reject fails the job | `internal/hitl/hitl_test.go::TestServiceApproveResumesJob`, `::TestServiceRejectFailsJob` | auto |
| 12 | Orphan Job Pod sweep on daemon boot | `internal/jobpod/manager_test.go::TestSweep_RemovesOrphans` (+ `Sweep_EmptyWhenNoContainers`, `Sweep_IgnoresForeignContainers`) | auto |
| 13 | Pod teardown removes the per-job token dir | `internal/jobpod/manager_test.go::TestStop_RemovesTokenDir` | auto |
| 14 | Power: battery/sleep pauses, AC/wake resumes | `internal/power/supervisor_test.go`, `policy_test.go`, `manager_test.go` | auto |
| 15 | Graceful shutdown stops pods, abandons in-flight phases, recovers next boot | [ADR-0060](adr/0060-shutdown-semantics.md) + rows 1–5 (`Recover`) | auto (via recovery) |
| 16 | A real `kill -9` of the running daemon mid-job | `spikes/m3-t7-probe soak -kill-after` → `soak-events.log` | opt-in (harness) |
| 17 | A real sleep/wake cycle mid-run | runbook [soak-m7.md](soak-m7.md) | **human** |
| 18 | 24h+ soak: no orphan pods, bounded RSS/WAL/disk, no unrecovered crash | `m3-t7-probe soak` (rolling `report.md`, `soak.csv`) | **human** (supervised run) |

## How to re-prove

```bash
make check                                   # rows 1–15 (structural + unit)
make test-integration                        # behavioral pod/gateway probes
go run ./spikes/m3-t7-probe soak -model <m> -corpus eval/bench/tasks.yaml \
    -hours 24 -kill-after 6h -out spikes/m3-t7-probe/results/soak   # rows 16, 18
```

Row 17 (sleep/wake) is performed by hand on the release host and recorded in the
soak run's notes; the harness cannot sleep the host safely.

## Gaps / notes

- Row 15 has no dedicated daemon-signal test; it is proven by the recovery rows
  (abandon = leave the job non-terminal; the next boot's `Recover` resumes). A
  signal-level test would need a running daemon and is covered behaviorally by
  row 16 during the soak.
- Rows 16 and 18 are produced by the M8-T23 harness on real hardware; their
  evidence lands under `spikes/m3-t7-probe/results/soak/` (gitignored).
