# ADR 0007 — Rootless Podman lifecycle for M2 Job Pods

**Status:** Accepted · **Date:** 2026-08-26 · **Refs:** ARCHITECTURE §21; ROADMAP M2-T1, M2-T2, M2-T3, M2-T6

## Context

M2-T1 is a 4-hour timeboxed spike to prove that rootless Podman
lifecycle (create, run, supervise, teardown) works on developer
machines with the §21.2 hardening flags, and that the §21.2
containment claims hold. The spike's output gates M2-T2 (Job Pod
manager) and M2-T3 (per-job tokens). The chosen invocation strategy
is recorded here so M2-T2 implements against a tested baseline.

## Decision

M2-T2 will shell out to the `podman` CLI from the daemon process, not
import a Go SDK. The spike (`spikes/podman-lifecycle/`) brings up a
hardened rootless container and verifies the §21.2 containment flags
in seven checks. All seven pass on macOS 14 + podman-machine
(podman 5.8.2, applehv). The spike is throwaway: it lives in
`spikes/` and is never imported by `internal/`.

The seven checks:

| # | Check | Result |
|---|---|---|
| 1 | Read-only rootfs (writes under `/etc` rejected) | PASS |
| 2 | Tmpfs at `/tmp` writable | PASS |
| 3 | Token visible at `/run/athanor/token` (tmpfs bind mount) | PASS |
| 4 | `wget http://example.com/` from inside the pod fails (network=none) | PASS |
| 5 | `/etc/hosts` is the container's own (not the host's) | PASS |
| 6 | No podman socket exposed at `/var/run/docker.sock` or `/run/podman/podman.sock` | PASS |
| 7 | cgroup `pids.max` reflects `--pids-limit=64` | PASS |

The invocation:

```
podman run --rm --name <job-id> \
  --network=none \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=8m \
  --security-opt no-new-privileges \
  --cap-drop=ALL \
  --pids-limit=64 \
  --memory=512m --cpus=1.0 \
  --mount type=bind,source=<token-dir>,target=/run/athanor,ro \
  -d <image> <command...>
```

Per-job tokens (M2-T3) are 16-byte random hex strings in a tmpfs bind
mount at `/run/athanor/token`, readable by the pod, never appearing
in env or argv. The token dir is created on the host with `0600`
permissions and unmounted at teardown. The internal API
(`execute_code`, `run_tests`) will validate the token against the
job ID it was issued for, on every call.

## Caveats

- **`--security-opt seccomp=...` is a no-op on rootless
  macOS/podman-machine.** Podman accepts the flag, but no kernel
  seccomp is available in the applehv VM. On a real Linux host the
  spike would set a default seccomp profile via the containers-common
  path (`--security-opt seccomp=/usr/share/containers/seccomp.json`):
  "runtime/default" is Docker/containerd syntax and Podman treats an
  unknown value as a file path, so it aborts with "no such file or
  directory". The spike's findings on macOS are explicitly weaker for
  this one flag. M2-T6's security suite is where Linux seccomp
  enforcement is verified.
- **Linux behavior was not exercised.** The spike was run on
  macOS 14 / podman-machine 5.8.2. The same `podman` CLI
  invocations are expected to work on Linux rootless with cgroups
  v2, but the spike did not run there. M2-T6 (security suite)
  must run on both Linux and macOS to close that gap.
- **The spike does not exercise teardown-on-crash.** A normal
  `--rm` exit path is tested; a `kill -9` of the daemon during
  the spike is not. M2-T5 (strict teardown + orphan cleanup,
  including a startup sweep) is the place to prove zero-surviving-
  pods semantics across crash, kill, and clean teardown.
- **`--pids-limit` is a cgroup max, not a shell `ulimit`.** The
  spike reads `/sys/fs/cgroup/pids.max` to verify the limit,
  because `ulimit -u` inside the container reports the host's
  per-user limit, not the cgroup max. The first version of the
  check conflated them; this is the corrected form.
- **M2-T1 is a 4-hour timebox.** The spike ran in roughly 15
  minutes once the podman-machine was started (it had been left
  in a bad state from a prior run; `podman machine stop &&
  podman machine start` recovered it). Future spikes should
  budget for machine-warmup time on first run.

## Consequences

- M2-T2 implements Job Pod lifecycle by shelling out to `podman`.
  The spike's seven-check table becomes the smoke test M2-T2
  must keep green; failing checks block M2-T6.
- M2-T3 (per-job tokens) implements the tmpfs bind mount mechanism
  proven by check #3. The token format (16-byte random hex) and
  path (`/run/athanor/token`) are fixed.
- M2-T6 (security suite) must run on both Linux and macOS to
  close the Linux seccomp and rootless-parity gaps noted above.
- The `internal/control` kill switch freezes the daemon; M2-T2
  must also implement "do not start new pods while frozen" by
  consulting the same `control.KillSwitch.Frozen()` surface the
  engine already uses.
