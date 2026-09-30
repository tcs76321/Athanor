# M2-T4b Plan — Engine ↔ Job Pod execution dispatch

**ADR:** [0024-engine-pod-dispatch.md](adr/0024-engine-pod-dispatch.md) ·
**Acceptance (ROADMAP M2-T4b / M2-T4):** *"Disallowed tools rejected and logged;
results streamed back with exit codes/output"* — with a real Job Pod in the
loop.

**Scope boundary (ADR-0024):** one pod per job + `podman exec` per tool call;
`execute_code` code delivered on stdin; the engine→internal-API hop is kept and
the 501s are replaced by a `PodExecutor` dispatch; the `job_pod.image`
fail-fast is enforced; Gate G2 grows an exec-argv arm. Out of scope: pod→daemon
callbacks, multi-pod execution, base-image packaging (M7-T7), per-task command
overrides.

## Commit sequence

Each commit: stage → `make check` → staged diff → one-line `M2-T4b.x:` message →
human signs.

### T4b.1 — ADR-0024 + this plan + ROADMAP task row (docs)

Lock before code: the push model (D1), one pod per job + `podman exec` (D2),
stdin delivery + closed command defaults (D3), keep the HTTP hop with a new
`PodExecutor` seam (D4), the honest token note (D5), the `job_pod.image`
fail-fast (D6), and Gate G2 exec-argv coverage (D7). No code, no gate ceremony.

### T4b.2 — `internal/jobpod`: exec capability + argv builder

- Add the `podman exec` argv builder in `internal/jobpod` (a new `args_exec.go`
  so the existing Gate G2 `args_*` scan covers it) and an `Exec`-shaped method
  on the `Manager` (or a small `Executor`) returning
  `{exit_code, stdout, stderr, duration_ms}`.
- Extend `jobpod.Client` (and `cmd/athanor/jobpod_client.go`) with an optional
  **stdin** channel so `execute_code` can pipe source to `python -` without
  putting it in argv.
- Unit tests for the argv shape (hardening flags preserved: `--read-only`,
  `--cap-drop ALL`, `--security-opt no-new-privileges`, `--network none`).
- Extend `TestGateG2JobPodArgvCannotEscape` to assert the exec argv carries no
  escape substring. Re-prove Gate G1.

### T4b.3 — `internalapi`: `PodExecutor` seam + real dispatch

- New `PodExecutor` interface in `internalapi` (nilable; nil → 503, mirroring
  `ToolGateway`/`ContextSwapper`), plus typed errors mapped to statuses.
- Replace the three 501s in `internalapi/exec.go` with dispatch: `execute_code`
  → `PodExecutor.RunCode`, `run_tests` → `RunTests`, `lint` → `Lint`.
- Handler tests: happy path (fake executor), envelope rejection (403 +
  `tool_disallowed`), not-configured (503), and an executor error mapping.
- Re-prove Gate G2 (route middleware + envelope-bypass structural tests still
  green with the new dispatch).

### T4b.4 — `cmd/athanor`: adapter + serve wiring + image fail-fast

- `cmd/athanor/pod_executor.go`: adapts `podjob` (the `jobpod.Manager`/Client)
  to `internalapi.PodExecutor`.
- Widen `internalapi.New(...)` and wire the adapter in `serve.go`.
- Enforce `job_pod.image` at boot (a non-empty image is required to dispatch);
  update the `config.go` / `config.example.yaml` note if the wording shifts.
- Boot test asserting the adapter is wired (the M4-T7 T5.3 precedent).

### T4b.5 — engine pod lifecycle

- Nilable engine seam (`PodLifecycle`/`PodManager` interface, adapter in
  `cmd/`) so the engine ensures the pod exists before the `evaluating` code
  sub-steps and stops it when the job reaches a terminal state.
- This closes the M2-T5/§23.6 recovery case: a resumed `code` job re-ensures its
  pod instead of failing on `ErrNotFound`.
- Tests: ensure-before-exec ordering with a fake; stop-on-terminal; nilable
  seam is a valid no-op. Re-prove Gate G1 (engine surface change).

### T4b.6 — close-out (docs)

- CHANGELOG entry; ROADMAP M2-T4b row → done; README status bullet;
  `docs/demo-m2.md` updated (the pytest clause is now end-to-end);
  ROADMAP §7 superseded row removed.
- Optional: an `ATHANOR_RUN_INTEGRATION=1` end-to-end probe documented alongside
  the existing M2 security probes.

## Risks / notes

- **Image/toolchain is a precondition.** The pod must have a shell + `python` +
  `pytest`/`ruff`. Until M7-T7 ships a base image, the operator supplies one and
  the plan records it; `serve.go` fails fast when it is missing.
- **Gate G2 structural edits are load-bearing.** The envelope check must remain
  a literal `a.tools.EnvelopeFor` reference in each exec handler (the gate is a
  text search); a refactor that hides it behind a helper trips the gate.
- **No new dependency, no migration, no new route** (the routes already exist).
  Gate ceremony is G1 + G2 re-proofs; the behavioral probe is opt-in.
- **M2-T4 acceptance is retracted until T4b.6.** The CHANGELOG/ROADMAP wording
  that overstates M2-T4 is corrected as part of the close-out, not left standing.
- One commit per logical change; one `-m`; GPG-signed by the human.
