# 0.0.0 security audit checklist

**Status:** in progress · **Date opened:** 2026-10-24 · **Last evidence run:** 2026-11-06 · **Refs:**
ARCHITECTURE §31.3, §21; [SECURITY.md](../SECURITY.md);
[ADR-0080](adr/0080-engine-executed-tools.md);
[ADR-0081](adr/0081-code-archetype-tool-defaults.md).

This is the §31.3 sign-off sheet for the 0.0.0 freeze. A row is checked only
when the named evidence has been run against the **Core Pod**, not just the
host development binary. Items marked **open** block the tag.

## Containment

- [x] Gate G1: no `os/exec`, container client, or raw `syscall` in `internal/`
      outside the named path wrappers — `internal/gate/gate_test.go`.
- [x] Gate G2: every internal route is auth-wrapped; every tool handler
      consults the per-job envelope — `internal/gate/gate_g2_test.go`.
- [x] Job Pod argv cannot add `--net=slirp4netns`, the Podman socket, or a
      host-FS bind-mount source — `TestGateG2JobPodArgvCannotEscape`.
- [x] Five behavioral pod probes (network, Ollama, podman socket, host FS,
      credentials) — `internal/jobpod/security_test.go`, gated, required CI.
- [ ] Same probes re-run against the released Core Pod image — **partial
      (2026-11-06)**: the image was rebuilt from the current tree, `make
      core-smoke` passed, and the full integration probe set passed on the same
      host (macOS, podman 6.0.2, ollama 0.40.15). A real inference job queued
      *inside* the Core Pod remains open: the host Ollama binds
      `127.0.0.1:11434` only and is not reachable through
      `host.containers.internal` on this machine.
- [x] Path containment: traversal/absolute/NULL/symlink/device/setuid corpus +
      fuzz — `internal/airlock/paths`.
- [x] Airlock ingress/egress adversarial corpus — `internal/airlock/*_test.go`.

## Gateway and content

- [x] SSRF corpus (CIDRs, rebinding, redirect hops, integer hosts) —
      `internal/gateway` + Gate G4.
- [x] Allowlist bypass corpus (typosquat, userinfo, trailing dot, scheme, CRLF,
      IDN) — Gate G4.
- [x] Injection corpus fail-closed through Reader Mode; zip/redirect bombs
      bounded — Gate G4.
- [x] Header allowlist + fixed User-Agent; response cap; pinned-IP dial;
      per-hop policy re-evaluation — `internal/gateway`.
- [ ] Gateway integration probes re-run against the Core Pod image — **partial
      (2026-11-06)**: the default-deny and allowlisted-fetch/extract probes pass
      from the host (`make test-integration`); running the probe binary from
      inside the rebuilt Core Pod is still open (the Core image does not ship
      the test binary or its toolchain).

## State, control, and recovery

- [x] Append-only event log enforced by triggers — `events_no_update` /
      `events_no_delete`.
- [x] Single-connection discipline — `internal/gate/gate_tx_test.go`.
- [x] Kill switch persists across restart; unfreeze requires a reason —
      `internal/control` + API/UI tests.
- [x] Crash recovery at phase boundaries; scheduler/HITL reconcile —
      `internal/engine/recovery_test.go`, scheduler tests.
- [x] Backup/restore drill — `internal/backup` `TestSnapshotPruneRestore`.
- [ ] 24h Core Pod soak with recovery/power injection — **open** (not run in
      the 2026-11-06 pass; it is a human, long-running checkpoint).

## API and UI

- [x] Loopback-only bind; Host allowlist; CSRF guard — `internal/server`.
- [x] Internal API per-job token, constant-time compare, scope isolation —
      `internal/internalapi` + `gate_scope_test.go`.
- [x] UI template escaping regression test — `internal/ui`.
- [ ] UI handler coverage for every interpolation/SSE path — **partial
      (2026-11-06)**: the package floor is now 65% (measured 69.0%), and the
      run-history/body-cap/state-seam paths have dedicated tests; exhaustive
      per-interpolation and SSE-path coverage is still outstanding.

## Supply chain and build

- [x] Direct-dependency allowlist — `internal/deps`.
- [x] `govulncheck` CI job.
- [x] `gofmt`/`goimports`, vet, race, lint in CI.
- [ ] Image digest/signature verification for the published Core Pod — **open**
      (1.0.0 distribution work; 0.0.0 builds from the repo).

## Evidence run — 2026-11-06

- `make core-image` from the current tree: built successfully.
- `make core-smoke`: Core Pod healthy, `/healthz`, `/ui`, and the API create
  path all pass.
- `make test-integration`: the five pod-hardening probes, the M2-T4b exec
  probe, and the two gateway probes all pass (`podman 6.0.2`, Ollama 0.40.15,
  macOS).
- `make check` and `make cover-check`: green; UI coverage 69.0% (floor 65%).

Still open above: in-Core probe execution, the 24h soak, the timed
fresh-install demo, and the 0.0.0 tag/sign-off.

## Sign-off

| Role | Name | Date | Notes |
|---|---|---|---|
| Maintainer | | | |

**Tag rule:** every **open** row must be closed or explicitly accepted as a
documented 0.0.0 limitation with an owner and a 0.0.x follow-up.
