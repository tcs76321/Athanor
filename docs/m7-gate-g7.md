# Gate G7 — release checklist

Gate G7 (ROADMAP §3): *System survives sleep, power loss, and 24h soak with no
state loss.* Below is the M7 close-out checklist. Items marked **automated**
are enforced by `make check` / CI; items marked **human** are supervised
checkpoints that cannot be fully automated.

## Security audit (ARCHITECTURE §31.3)

| Check | Status | Evidence |
|---|---|---|
| Symlink escape attempts | automated | `internal/airlock/paths` adversarial corpus + `O_NOFOLLOW` behavioral test |
| Path traversal payloads | automated | `TestGateG1NoToolExecution`, airlock path property test |
| Prompt-injection payloads in uploaded documents | automated | `internal/airlock/scanner` injection corpus |
| Podman socket misuse from Job Pods | automated (opt-in) | `internal/jobpod/security_test.go` (gated `ATHANOR_RUN_INTEGRATION=1`; required CI `integration` job) |
| Credential leakage (secrets never in pod env) | automated (opt-in) | `internal/jobpod/security_test.go` credential probe |
| Kill switch activation and frozen-mode enforcement | automated | `internal/control`, `internal/engine` freeze tests; critical alarms freeze |
| Egress only via the gateway allowlist | automated | Gate G1 rule 6; `internal/gateway` SSRF/bypass corpora |
| No tool execution outside a hardened Job Pod | automated | Gate G1 + Gate G2 structural tests |

Sign-off: a human reviews the above and the M4/M2 behavioral probe runs
(`make test-integration`) on release hardware, then records the date and
result here. **Status: pending human sign-off.**

## Crash / power-loss recovery

| Check | Status | Evidence |
|---|---|---|
| Kill-9 mid-job resumes from last committed state | automated | M3-T6 E2E recovery tests; `internal/job` recovery tests |
| Migrations are forward-only, idempotent, backed up | automated | `internal/store` migration tests; Gate G0 |
| Sleep/wake pauses and resumes | human | macOS/Linux observer + `ResumePaused`; verify with a real sleep cycle |

## 24-hour soak (M7-T9)

Run [`soak-m7.md`](soak-m7.md) on AC hardware; the results are the evidence.
**Status: pending human run.** Acceptance: no orphan pods, no unrecovered
crashes, bounded RSS/goroutine growth, bounded WAL/disk.

## Fresh-install demo (<15 minutes)

Run `scripts/install.sh` on a clean machine, then follow
[`demo-m7.md`](demo-m7.md). **Status: pending human run.**

## Gate re-proof

```bash
make check                                  # lint + vet + race
CGO_ENABLED=1 go test ./internal/gate/      # Gate G1 (+ G2 structural)
make test-integration                       # behavioral probes (podman + internet)
```

G0–G6 remain enforced by their in-tree tests; this file tracks the M7-specific
close-out.
