# ADR 0050 — First-run doctor

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §30.2;
ROADMAP M7-T5; [ADR-0029](0029-file-io-containment-scope.md) (Gate G1
allowlist shape)

## Context

ROADMAP M7-T5 requires `athanor doctor`: a single command that validates the
host, the inference backend, and the configuration before the daemon is
trusted with work, and that catches each seeded fault (no Podman, no Ollama,
a missing persona model, low RAM) with actionable output.

Two constraints shape the design:

1. **Gate G1 containment.** `internal/` may not import `os/exec`, and `cmd/`
   may import it only in named files (`jobpod_client.go`, `git_client.go`).
   Diagnostics inherently need to run `podman info`, `df`, `sysctl`, and to
   read `/proc/meminfo`, so the OS boundary must not leak into a shared
   package.
2. **Testability without a host.** The checks must be exercisable without
   Podman, Ollama, or a particular OS so CI can seed a fault per check.

## Decision

Split the feature along a **Probes seam**:

- `internal/doctor` holds the pure orchestration: the check list, severity
  policy, `modelPresent` matching, and the report. It reads the world only
  through the `doctor.Probes` interface, so `internal/` stays free of
  `os/exec`, `syscall`, and direct network I/O.
- `cmd/athanor/doctor.go` implements `Probes` with `os/exec`, a constructed
  `*http.Client`, `runtime.GOOS`-split memory reads, `df -k`, and a temp-file
  writability probe. It is added to `allowedOsExecFiles` in
  `internal/gate/gate_test.go` (the deliberate, reviewable act Gate G1
  requires).

Severity policy:

- **fail** — a hard prerequisite: Podman (rootless), Git, Ollama reachable,
  every persona model installed, a writable state directory. The daemon
  cannot do useful work without these.
- **warn** — advisory: memory below 8 GiB, disk below 5 GiB, an empty
  `job_pod.image`, a context target below the code-archetype floor, a
  non-deny `network.default_policy`.

The context-floor check is deliberately a **warn**, not a fail: §12.6 floors
bind at runtime (the engine pauses the job), and an operator may intentionally
under-provision non-code work. Doctor surfaces the tension rather than
blocking boot. The shipped default config does trip this warning for `tall`
and `security` (targets 16384/8192 vs the 32768 code floor), which is the
honest signal §12.6 predicts.

## Consequences

- `athanor doctor` is a pure function over an injected host, with a seeded-
  fault test per severity.
- The pattern generalises: future OS-touching features (the M7 power watcher,
  the backup scheduler) can follow the same "pure internal/ package + cmd/
  adapter + Gate G1 allowlist entry" shape.
- The remediation strings are the operator's next action; a missing model
  emits the literal `ollama pull <model>` command.
