# Known issues

A running log of findings from code review, with dispositions. This is not the
roadmap backlog (`ROADMAP.md` §7) — the items here are defects or hardening
gaps, not deferred features.

**Review:** 2026-10-06, static read-only review of `internal/` and `cmd/`
(no builds/tests run during the review). Findings verified by hand before
recording; each entry names its location and status.

---

## Open

None.

---

## Fixed (2026-10-07)

| Finding | Resolution |
|---|---|
| O1: a gateway policy denial (403) was indistinguishable from a tool-envelope violation | a policy denial is now `451` (`internalapi.ErrFetchDenied`) → `toolenvelope.ErrPolicyDenied` in the runner; the engine audits `outcome=denied` distinctly. Contract in [ADR-0019](adr/0019-gateway-tools.md) §1/§7; tests in `internalapi/gateway_tools_test.go`, `runner/httpclient_test.go`, `engine/research_test.go`. |
| O2: `GET /internal/v1/jobs/{id}` reported a hardcoded `State: "running"` | `handleJobGet` now returns the job's real state via `Repo.JobTask` (which also fixes F-3: job → task resolution). Tests in `internalapi/handlers_test.go`. |

## Fixed (2026-10-06 review)

| Finding | Resolution |
|---|---|
| Job Pods were created `--name <uuid>` but `Sweep` filtered `name=athanor-job-`, so crash orphans were never swept | `--name athanor-job-<uuid>` (`d0d884a`) |
| `Stop` removed the container but never cancelled `supervise`, leaking a goroutine + a failing `podman inspect` every 2 s | per-entry `context.CancelFunc`, cancelled in `Stop` (`d0d884a`) |
| `Start`'s duplicate-ID check and map insert were not atomic | reserve the ID under the lock, release on failure (`d0d884a`) |
| `airlock/paths.Validate` checked symlink escape only when the **final** component was a symlink; a symlinked directory under root escaped | always `EvalSymlinks` and require containment in the canonical root (`5052ecf`) |
| `logging` rotation left `w.f == nil` on a rename/reopen error → permanent write failure | reopen the original (or the rotated file) on failure (`95d6ae4`) |
| `gateway` per-host rate-limiter map grew without bound | idle + LRU eviction with a 1024-bucket cap (`95d6ae4`) |
| `backup` re-fired a failed scheduled job every 30 s within the same minute | mark the attempt before running (`95d6ae4`) |
| Engine failure path transitioned to `failed` without clearing the §10.5 suppression row | `clearSuppressedTiers` on the failure path (`155cc02`) |
| Probe config: an out-of-matrix `-judge-model` inherited the generator's family, silently satisfying the cross-family guard | fall back to the declared family or empty, never the generator's (`155cc02`) |

## Checked and clean

Gate G1 containment (no `os/exec` in `internal/`; `syscall` only in the
allowlisted `airlock/paths` build-tag wrappers), per-job token auth (CSPRNG,
constant-time compare, URL binding), the job state machine (legal-edge table +
CAS transition + atomic audit), the gateway (allowlist→resolver ordering,
`.`-anchored suffix match, deny-list precedence, private-IP guard, pinned-IP
dial, per-hop re-evaluation, size cap), the server (Host allowlist, cross-site
guard, auth-wrapped internal routes), tool-envelope enforcement on every
handler, and row/body/token-dir cleanup.
