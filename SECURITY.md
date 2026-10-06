# Security Policy

Athanor is a local-first agent system that executes untrusted model output
inside hardened, ephemeral containers and gates every external action. Its
security posture is enforced by executable gates (see `internal/gate/`) and
adversarial corpora (see `internal/gateway/*_test.go`) rather than by
convention, so security reports are treated as first-class.

## Supported versions

Athanor is pre-MVP; only the tip of `main` is supported. There are no
maintained release branches yet.

## Reporting a vulnerability

Please report suspected vulnerabilities privately, not in a public issue:

- Use GitHub's private vulnerability reporting on the repository
  (**Security → Report a vulnerability**), or
- Email the maintainer listed in the repository profile.

Include, where possible: affected component, a minimal reproduction, the
commit or version, and the impact you believe it has. If you are unsure
whether something is in scope, report it anyway.

Do **not** include live credentials or third-party personal data in a
report. If a proof of concept requires them, describe the shape instead.

## What to expect

- Acknowledgement within a few days.
- An assessment and, when confirmed, a fix with a regression test and a
  `CHANGELOG.md` entry (the project records security fixes there, e.g. the
  `golang.org/x/net` CVE-2026-25680 bump).
- Credit in the changelog unless you ask to remain anonymous.

## Scope notes

- Job Pods are rootless, read-only-rootfs, `--network=none`, and receive
  only a per-job bearer token; reports that they can reach the host, the
  network, Ollama, the Podman socket, or credentials are high interest.
- The §21.5 gateway is the only outbound HTTP path; SSRF and allowlist
  bypass reports are high interest.
- The §21.3 airlock path-containment layer and the scanner pipeline are in
  scope.
- The loopback HTTP surface is reachable by any page the user visits, so
  cross-site request forgery (state-changing requests triggered by a third
  site) is in scope. The Host-header allowlist ([ADR-0011](docs/adr/0011-external-api-host-allowlist.md))
  closes DNS rebinding; the `Sec-Fetch-Site`/`Origin` guard
  ([ADR-0057](docs/adr/0057-cross-site-request-defense.md)) closes CSRF.
- The daemon binds loopback only; remote-access configuration is out of
  scope.
