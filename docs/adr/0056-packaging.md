# ADR 0056 — Packaging and headless install

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §21.2
(Job Pod hardening), §30.2 (doctor), §32 (deployment); ROADMAP M7-T7, M7-T8

## Context

ARCHITECTURE §32 describes a single CLI that provisions rootless Podman and a
Core Pod OCI image; DEVELOPMENT.md says the Core runs as a host binary during
development. M7-T7/T8 must ship something usable without over-building
container orchestration, and must support headless operation (systemd/launchd,
UI via SSH forwarding).

## Decision

Ship the **host-binary** shape:

- **Job Pod image** (`deploy/jobpod.Containerfile`): a Python 3.12 base with
  git/build-essential/pytest, a non-root user, no network at run time. Built
  with `make jobpod-image`; referenced by `job_pod.image`.
- **Single binary + install script** (`scripts/install.sh`): builds/installs
  `bin/athanor` into `PREFIX` (default `/usr/local/bin`), writes a default
  config, and with `--service` renders a service unit.
- **Headless**:
  - Linux: a systemd **user** unit (`deploy/athanor.service`) installed to
    `~/.config/systemd/user/athanor.service`.
  - macOS: a launchd **agent** (`deploy/com.athanor.agent.plist`) installed to
    `~/Library/LaunchAgents/`.
  - The daemon binds loopback only (§21.8); remote UI is reached via SSH port
    forwarding.
- **`athanor start`**: runs the §30.2 doctor, refuses on a hard prerequisite
  failure, then serves. `install.sh` points a fresh machine at it.

Placeholders (`__BINARY__`, `__CONFIG__`, `__STATE__`) are substituted by
`install.sh`; `internal/deploy`'s test asserts the templates exist and carry
those markers, so a rename cannot silently break the installer.

## Consequences

- No Core Pod OCI image is published in this milestone. The daemon runs as a
  host binary under systemd/launchd; Job Pods still ship as OCI images. A
  Core-image deployment can be layered on later without changing the daemon.
- The fresh-install demo (<15 min) and the §31.3 security-audit sign-off are
  human checkpoints (ROADMAP Gate G7), not automated by this ADR.
