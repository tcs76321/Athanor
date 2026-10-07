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

## Deliberate trust boundaries

These are intentional design boundaries, documented so a reader does not
mistake them for gaps:

- **Pod-supplied commands run inside the pod.** `run_tests` and `lint` accept a
  command from the job (an operator/project command), which executes *inside*
  the already-hardened Job Pod. It grants no capability the pod did not
  already have; the pod remains rootless, read-only-rootfs, `--network=none`,
  with no host mounts beyond the approved one. `execute_code` language is
  closed to `python`.
- **The cross-site guard allows non-browser clients.** A request carrying
  neither `Sec-Fetch-Site` nor `Origin` is treated as a non-browser client
  (the CLI, a Job Pod, curl) and allowed past the CSRF guard. Those callers
  hold no ambient browser authority, and are separately bounded by the
  loopback-only bind, the Host-header allowlist (ADR-0011), and — for pods —
  the per-job bearer token (ADR-0008).
- **Internal API status contract.** A tool that is not in the job's envelope
  is `403` + `ErrToolDisallowed`; a destination the §21.5 gateway policy
  refuses is `451` + `ErrPolicyDenied`. The two are deliberately distinct so
  a caller can tell an envelope miss from a containment denial (known-issues
  O1; [ADR-0019](docs/adr/0019-gateway-tools.md)). No pod-supplied identifier
  may select a data scope — enforced by `internal/gate/gate_scope_test.go`.
