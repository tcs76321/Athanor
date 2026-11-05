# ADR 0099 — Measured model fit and the startup guard

**Status:** Accepted — first implementation slice · **Date:** 2026-11-04 ·
**Refs:** ARCHITECTURE §12.3 (context), §24 (resources);
[ADR-0049](0049-model-onboarding-qualification.md) (qualification),
[ADR-0096](0096-capacity-aware-concurrency.md) (capacity),
[ADR-0098](0098-adaptive-job-pod-limits.md) (sandbox ceilings).

## Context

A model's file size is not its resident size. At a 32K context Ollama keeps
weights **and** a KV cache resident. Measured on the reference host:
`granite4.2:3b` is 2.2 GiB on disk but **4.78 GiB resident** at 32K. The old
model-fit budget used raw file sizes and a fixed 2 GiB reserve, so it could
call a model "fits" when it would spill into swap or evict another model.

Two other guarantees were missing:

- Nothing checked, after configuration changed, that the configured persona
  models still fit the host. The onboarding budget was shown once and forgotten.
- "Best model" is only meaningful from the qualification ladder's per-role
  measurements; file size alone is a capability proxy.

macOS cannot hard-reserve host RAM for Ollama, so the honest controls are the
Podman runtime allocation, one-resident-model policy, and measured fit.

## Decision

1. **Measure, do not estimate.** `athanor models -audit` loads each configured
   persona model at its largest context target with a one-token prompt, reads
   the actual resident size from Ollama `/api/ps`, asks Ollama to unload it, and
   writes `model-fit.json` next to the config. It reports file size, loaded
   size, context, and fit per role.
2. **Budget the host, not the VM.** The audit budget is host physical memory
   minus an explicit reserve (default 8 GiB) for macOS, other apps, and the
   Core. A role fits only when its measured resident size is at or below that
   budget. Job Pods are a separate runtime budget.
3. **Enforce the audit at startup.** `athanor start` reads `model-fit.json`,
   verifies that every configured role/model/context is covered, and refuses to
   start when a measured model does not fit. A missing audit is a warning; a
   stale audit (host memory changed, or config diverged from the measured
   roles) is rejected. `-skip-model-audit` is the explicit escape hatch.
4. **Pass the plan to the Core.** The same JSON is passed as
   `ATHANOR_MODEL_AUDIT` and shown on the Setup page, so the operator sees the
   exact plan the startup guard enforced rather than a second estimate.
5. **Keep one model resident.** The audit reports models still loaded at the
   end. The operator guidance is `OLLAMA_MAX_LOADED_MODELS=1` and
   `OLLAMA_NUM_PARALLEL=1`; Athanor already serializes model calls with
   `max_concurrent_llm_calls=1`. A shared model across generator roles is
   preferable to frequent cross-family swaps when the budget is tight.
6. **The audit decides fit, qualification decides quality.** The audit never
   silently rewrites persona config. Qualification measurements and the
   cross-family judge rule still choose the best model; the audit only rejects
   a measured non-fit. Applying a new model remains a reviewed config change.

## Consequences

- A run cannot silently start with a model that measured over budget.
- The audit is explicit and expensive (it loads each configured model), so it
  is an operator action, not a startup side effect. The startup guard is cheap.
- `model-fit.json` is host-side state next to the config; the Core receives the
  plan through its environment, so the UI can display it without another mount.
- A model can still be paged by macOS under pressure; the guard makes the
  budget honest, not a hard reservation.
- Disk remains a Podman VM concern, separate from this audit.

## Alternatives rejected

- **Estimate KV from file size.** Wrong direction and wrong magnitude; measured
  `granite4.2:3b` at 32K nearly doubles. Measurements are the only defensible
  number.
- **Auto-swap models at startup.** Silent config mutation; the operator would
  not know which model produced the work. Recommendations stay advisory and
  applying one is explicit.
- **Fail when no audit exists.** A fresh install has no measurement yet; that
  should be a warning with a clear command, not a blocked boot.
- **Read only Ollama `/api/tags`.** It reports the blob size, not the resident
  weights + KV size at the configured context.
