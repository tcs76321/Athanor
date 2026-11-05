# ADR 0096 — Capacity-aware concurrency, not a single-agent invariant

**Status:** Accepted — first slice · **Date:** 2026-11-04 ·
**Refs:** ARCHITECTURE §24 (resource management), §12.3 (context floors);
[ADR-0072](0072-single-slot-execution.md) (single-slot execution),
[ADR-0078](0078-core-pod-only-runtime.md) (Core Pod),
[ADR-0083](0083-repository-working-set-pack.md) (working set).

## Context

ADR-0072 made single-slot execution an invariant so one local model stays
resident and its context budget is not silently divided. That is the right
default for the common machine (one GPU or unified memory, 16–32 GiB), but it
is not a property of Athanor itself.

The engine already separates two resources:

- **Job concurrency** — how many jobs may be in flight (pod work, phases,
  verification).
- **Inference concurrency** — how many chat/embedding calls may run
  (`limits.max_concurrent_llm_calls`).

A machine with more RAM/VRAM can overlap a test suite with another job's
planning, or run several candidates concurrently. A constrained machine must
serialize inference so one model remains loaded and `num_ctx` can be spent on
the task rather than `OLLAMA_MAX_LOADED_MODELS` swapping models in and out.

The operator-facing mistake to avoid is making “one agent” a law. The
defaults should be conservative; the ceiling should be a configuration
decision.

## Decision

1. **Keep the default serialized.** `limits.max_concurrent_jobs` and
   `limits.max_concurrent_llm_calls` default to `1`. The shipped first-use
   experience is still single-slot and one resident model.
2. **Make `limits.max_concurrent_jobs` real.** It is a hard ceiling on
   in-flight jobs. The power profile may reduce it while the machine is
   interactive; it never raises it above the operator’s value.
3. **Keep inference serialization separate.** `max_concurrent_llm_calls`
   bounds chat/embedding calls independently. On a single-GPU host it should
   stay at `1`; the model stays resident and the dialectical engine can give
   that call the largest context it asks for.
4. **Document the host prerequisites for concurrency.** A host that raises
   either value must also set `OLLAMA_MAX_LOADED_MODELS` and
   `OLLAMA_NUM_PARALLEL` consistently, and must have RAM/VRAM for the resident
   models. `doctor` reports the deployment shape, installed persona models,
   memory, disk, and (when run on the host) the GPU; the operator remains
   responsible for the Ollama environment variables.
5. **Do not promise multi-agent behavior yet.** Multiple concurrent jobs are
   orchestration concurrency, not independent agents with separate memories.
   A future multi-agent mode (isolated contexts, specialist agents, token
   wasting for exploration) is a separate feature and budget policy.
6. **Large hardware is a first-class target.** The
   [Linux/GPU deployment guide](../linux-gpu.md) documents the 32 GiB / 1080 Ti
   class explicitly: one model at a time with a large context,
   `max_concurrent_jobs` tuning, and the trade-off between context,
   quantization, and GPU offload. Job concurrency above one requires the
   **autonomous** power profile (AC power and idle); the interactive profile
   throttles back to one job.

## Consequences

- The single-slot ADR is amended, not deleted: its default and rationale
  remain.
- Operators can tune concurrency without forking the engine.
- The engine's `power.Ceiling` wraps the configured ceiling and the live power
  profile, so interactive use still throttles to one job.
- Raising job concurrency without raising inference concurrency is safe and
  useful (one job can be in a pod while another plans); raising both requires
  matching Ollama and memory configuration.
- The benchmark today pins `max_concurrent_jobs: 1` in its generated config,
  and the frozen v2 protocol cannot be edited. Any future run with a different
  ceiling must record it in the next corpus version's protocol, because it
  changes latency and token behavior; until then concurrency is not a benchmark
  variable.

## Alternatives rejected

- **Hard-code concurrency from detected hardware.** Detection is noisy
  (cgroups, unified memory, shared VRAM) and an automatic guess can thrash the
  host. Explicit ceilings with conservative defaults are auditable.
- **Always allow N jobs and let the LLM semaphore block them.** That consumes
  goroutines and phase slots without doing useful work; the job ceiling is a
  separate, cheap control.
- **Call N concurrent jobs “multi-agent.”** They share one Core, one memory,
  and one policy engine. Calling that multi-agent would overstate the system.
