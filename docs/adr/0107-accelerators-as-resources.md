# ADR 0107 — Accelerators as first-class inference resources

**Status:** Accepted · **Date:** 2026-11-06 ·
**Refs:** ARCHITECTURE §12.3 (context feasibility), §24 (resources);
[ADR-0099](0099-measured-model-fit.md) (measured fit),
[ADR-0102](0102-separate-host-and-runtime-memory-budgets.md) (host/runtime
split), [ADR-0106](0106-adaptive-host-reserve-floor.md) (host reserve).

## Context

Athanor's capacity planning treated every host as if inference ran on the CPU
from host RAM. On Apple Silicon that is close enough: the GPU shares unified
memory, so the host budget is the right denominator. On a Linux machine with a
discrete GPU it is wrong. A 32 GiB host with an 11 GiB GPU and a 19 GB model was
budgeted against 28 GiB of host RAM, so the model was called a fit even though
it would spill most of its layers to system RAM and lose most of its speed.

The Core Pod cannot see the host GPU: the image has no `nvidia-smi`, no GPU
device is mounted, and the Podman VM hides it on macOS. The host doctor already
detected an NVIDIA GPU, but that row was display-only. Nothing passed VRAM into
the Core's fit or context planning.

An NPU is different: Apple's Neural Engine and the current Intel/AMD NPUs are
not scheduled by the Ollama backend. Detecting one and calling it an inference
resource would be a false claim.

## Decision

1. **The host probes accelerators and passes the result to the Core.** `athanor
   start` probes CUDA/NVIDIA, ROCm/AMD, and Metal/Apple, plus NPU presence, and
   passes a JSON record to the Core (`ATHANOR_ACCELERATOR`), the same shape as
   the model audit. A Core without the record simply omits the accelerator UI.
2. **Discrete GPUs budget VRAM, not host RAM.** The full-speed model budget is
   `VRAM − display/compositor headroom` (10%, clamped to 0.5–2 GiB). A model
   above that is reported as **partial offload / spill**, with the spill size,
   not as a host-RAM fit.
3. **Unified accelerators keep the host budget.** Apple Silicon's GPU shares
   host memory; the existing host-reserve budget remains correct. The UI labels
   it as unified rather than pretending there is no GPU.
4. **One fit limit feeds every consumer.** The pool's context capacity,
   auto-assignment, and qualification use the effective fit limit (GPU budget
   when discrete, host budget otherwise), so the picker cannot recommend a
   model that only runs by spilling.
5. **NPU is inventory, not a schedule target.** The doctor and Resources show a
   detected NPU and state that the current backend does not use it. It enters
   budgeting only when a backend exposes it.
6. **Job Pods do not get accelerators by default.** Untrusted code must not see
   the GPU/NPU. GPU-enabled data/ML tasks are a future opt-in capability with
   CDI device exposure and HITL approval; the default Job Pod stays CPU-only.
7. **No silent overcommit.** Multi-GPU devices and VRAM are displayed; the
   planner uses one device's VRAM unless a backend-specific tensor-split policy
   exists.

## Consequences

- Linux GPU hosts get an honest model-fit number and an explicit spill warning
  instead of a host-RAM number that hides offload.
- Apple Silicon is labeled correctly (unified GPU) and keeps its current budget.
- The Core remains unable to see the device; the host probe is the source of
  truth, and a missing probe degrades to the previous CPU/host behavior.
- NPU presence is visible for planning/inventory without a capability claim.
- GPU passthrough to Job Pods remains intentionally out of scope; adding it is
  a security change, not a resource-plumbing change.

## Alternatives rejected

- **Keep host-RAM budgeting on discrete GPUs.** Materially wrong; it recommends
  models that spill and hides the performance cliff.
- **Let the Core Pod see the host GPU.** Mounting devices into the trusted Core
  would not help budgeting and would widen its surface; the host probe is
  enough.
- **Treat NPUs as available.** No supported backend schedules onto them today.
- **Pass GPUs into Job Pods by default.** Untrusted code must not inherit host
  accelerator access; the default stays CPU-only.
