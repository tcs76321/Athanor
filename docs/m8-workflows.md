# M8 real-world-internal workflows

**Scope:** the end-to-end workflows 0.0.0 must actually perform — all local, with
no external account/egress beyond the §21.5 gateway allowlist. Each row names
its evidence. Maintained by M8-T25.

"Real-world but not external usage" is the boundary: the agent does real work on
the machine and over allowlisted read-only domains; it never uploads user data,
never uses cloud inference, and never runs Browser Mode.

| Workflow | What completes end-to-end | Evidence |
|---|---|---|
| **Code (single-module)** | goal → candidates → real tests in a Job Pod → accept → git commit | corpus code tasks (`eval/bench/tasks.yaml`, M8-T17); `internal/engine` code tests; `docs/demo-m2.md` |
| **Code (multi-file / refactor)** | candidate tree + fixture overlay → tests → accept → **tree** commit | M8-T17 (ADR-0065, `candidateFiles`, `readFixtureTree`, `CommitTree`); `code-bank-atomic`, `code-refactor-provided` |
| **Document / text / data** | goal → artifact → deterministic structural checks | `eval/bench/tasks.yaml` + the M8-T18 check engine |
| **Research (allowlisted)** | task-declared URLs → §21.5 gateway → Reader Mode markdown → cited artifact | `docs/demo-m4-t7.md`; `internal/gateway/integration_test.go` (opt-in); ADR-0019 §7 |
| **Brainstorm** | exploratory goal → scored option set | ARCHITECTURE §15; corpus text tasks approximate it (M8 straggler: a dedicated brainstorm corpus task) |
| **Daydreaming** | idle → memory consolidation / repo exploration / docs / feedback review / strategy mining, draft-only | M7-T2 (ADR-0052); `internal/daydream` tests; `docs/demo-m7.md` |
| **Morning digest** | window summary of jobs/artifacts/approvals/alarms/daydream/tokens | M7-T6 (ADR-0055); `internal/digest` tests; `docs/demo-m7.md` |
| **HITL approve/reject/defer** | external/irreversible action parks and resumes on decision; expiry denies | `internal/hitl` tests; ADR-0035/0036 |
| **Power / sleep-wake** | pause on battery/sleep, resume on AC/wake | `internal/power` tests; [m8-process-matrix.md](m8-process-matrix.md) |

## What the soak exercises vs. what stays demo/unit

The M8-T23 `soak` runs the **corpus** (code, document, text, data, adversarial)
against one long-lived daemon, so the first, second, third, and (partly) fifth
rows run repeatedly over 24h and produce quality + endurance evidence. The
research, daydream, digest, and HITL rows are proven by their unit/integration
tests and the M7 demo; folding them into the soak is optional (they need
allowlisted network or an idle window, and the soak is a continuous-load run).

## Explicitly out of scope (external / irreversible)

- Cloud inference (§21.7 credential broker) — design only.
- Browser Mode (§21.6) — HITL-gated, deferred.
- `git push` to a remote — HITL-gated (ADR-0036).
- Any user-data egress.
