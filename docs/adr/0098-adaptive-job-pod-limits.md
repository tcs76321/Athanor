# ADR 0098 — Adaptive Job Pod limits, not operator-facing sandbox knobs

**Status:** Accepted — first implementation slice · **Date:** 2026-11-04 ·
**Refs:** ARCHITECTURE §24 (resource management); [ADR-0093](0093-project-capabilities-not-archetypes.md)
(task capabilities), [ADR-0095](0095-first-class-gui-and-onboarding.md)
(onboarding), [ADR-0096](0096-capacity-aware-concurrency.md) (capacity).

## Context

The first-run Resources step exposed the raw Job Pod knobs: memory, CPUs,
`/tmp`, and PID limit. Those are sandbox implementation details. They also
created a false expectation that the step allocates the whole machine. It does
not: Job Pods run inside the Podman VM on macOS, while Ollama runs on the host.

The result was confusing in both directions. An operator could not set 32 GiB
because the runtime was smaller, and the model-fit warning compared host-side
Ollama memory with the container runtime's memory. The knobs also implied that
every task needs the same sandbox, although a code task running builds/tests
needs far more than a short text task.

macOS cannot reserve host RAM for a process. Any claim that a slider "keeps"
memory free for Ollama would be false; the OS may compress or swap. The honest
controls are the container runtime allocation (Podman VM) and a model-fit
budget, plus advice to leave host memory free.

## Decision

1. **Treat `job_pod` limits as a ceiling.** `job_pod.memory_mb`, `cpus`,
   `tmpfs_mb`, and `pids_limit` describe the largest sandbox Athanor may give a
   task. They are not a per-job fixed size.
2. **Size each Job Pod from the task's primary capability.** At pod start,
   Athanor looks up the job's task and scales the ceiling:
   - `code` / `media`: 100% (builds, tests, media tooling);
   - `data`: 75%;
   - `document`: 50%;
   - `text`: 40%;
   - unknown/legacy: 75%.
   Memory and `/tmp` round to 512/256 MiB, CPUs to 0.25, PIDs to a whole
   number, with floors (512 MiB, 0.5 CPU, 256 MiB `/tmp`, 64 PIDs).
3. **Derive the sandbox internals.** The onboarding UI does not offer CPU,
   `/tmp`, or PID fields. It shows the adaptive ceiling and the derived values,
   and writes them with one explicit action. CPU is capped at four cores for a
   single pod; `/tmp` scales with the memory ceiling; PIDs default to 512.
4. **Show the two memory layers honestly.** The Resources step reports **runtime
   RAM** (the Podman VM, which bounds Job Pods), **host RAM**, and the amount
   outside the runtime for macOS and Ollama. It says macOS cannot reserve host
   RAM and tells the operator to keep as much host memory free as possible.
   The Models step compares model + Job Pod + reserve against **host** memory.
   **Superseded by [ADR-0102](0102-separate-host-and-runtime-memory-budgets.md):**
   configured-model inference and runtime sandbox budgets are now separate, and
   the Podman VM is counted as a host allocation.
5. **Keep the enforcement boundary explicit.** Athanor enforces the runtime and
   Job Pod limits. Ollama's host footprint is a budget contract: one resident
   model (`OLLAMA_MAX_LOADED_MODELS=1`, `OLLAMA_NUM_PARALLEL=1`), a model and
   context target that fit, and an advisory model-fit check. No process-level
   host reservation is promised.
6. **Keep a manual ceiling for advanced hosts.** An operator may raise the
   `job_pod` ceiling in `config.yaml`; the adaptive fractions still apply below
   it. The onboarding action recomputes the ceiling from runtime capacity.

## Consequences

- A document or text task no longer reserves the full build/test sandbox, so
  more work fits alongside the Core and a resident model.
- A code task still gets the full configured ceiling, which is where tests and
  package builds need it.
- The onboarding UI stops pretending to reserve the host. The only hard fences
  are the Podman VM/runtime and Job Pod limits.
- A pod whose task cannot be loaded falls back to the configured ceiling (or the
  conservative 75% if the task has no capability).
- Disk remains a Podman VM concern. A grow-only VM disk cap is a separate
  host-level operation; this slice does not claim to enforce a host memory
  reservation.

## Alternatives rejected

- **Keep exposing all four knobs.** They are internal, invite meaningless
  combinations, and imply a host reservation Athanor cannot make.
- **One fixed sandbox for every task.** It wastes memory on text/document work
  and starves code tasks when the ceiling is reduced.
- **A host-memory slider that "reserves" RAM for Ollama.** macOS has no
  cgroup-equivalent; the slider would be a lie. The runtime allocation and
  model-fit budget are the enforceable controls.
- **Auto-loading a larger model to use spare memory.** Spare host memory is for
  macOS and other apps; Athanor should not consume it speculatively.
