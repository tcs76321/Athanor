# ADR 0102 — Separate host inference and runtime sandbox budgets

**Status:** Accepted · **Date:** 2026-11-05 · **Refs:** ARCHITECTURE §12,
§24; [ADR-0096](0096-capacity-aware-concurrency.md),
[ADR-0098](0098-adaptive-job-pod-limits.md),
[ADR-0099](0099-measured-model-fit.md).

## Context

The onboarding Models step displayed:

> Largest installed model 21.2 GiB + Job Pod 3.5 GiB + 2.0 GiB reserve vs
> 32.0 GiB host RAM — fits

That equation was wrong in three ways:

1. It used the **largest installed model**, including models no persona is
   configured to use.
2. It added the **Job Pod ceiling** (memory inside the Podman runtime) to the
   host model budget as if it were a second host allocation.
3. It ignored the **Podman VM** itself, even though on macOS that VM is a real
   host allocation. The screen could say "fits" while model + VM + OS reserve
   exceeded host RAM.

## Decision

1. **Two independent budgets.**
   - **Host inference:** the largest *configured persona* resident size
     (measured from the audit when available, file size otherwise) against
     `host RAM - OS/apps reserve (8 GiB) - Podman VM allocation` when the VM is
     a separate allocation.
   - **Runtime sandbox:** the Job Pod ceiling against `runtime RAM - runtime
     reserve`.
2. **Installed-but-unused models do not count.** The budget follows
   `personas.*`, not the model catalog.
3. **Combined host pressure is reported separately.** If configured model +
   Podman VM + OS reserve exceeds host RAM, the UI says so even when the model
   alone fits the audit's inference budget.
4. **The measured audit and the UI use the same 8 GiB host reserve.** The
   audit's "fits" means "fits the inference budget"; the UI additionally shows
   the runtime allocation and the combined host pressure.

## Consequences

- An operator can keep a large model installed without it silently shrinking
  the budget; only configured personas count.
- A 30B model on a 32 GiB host with an 8 GiB Podman VM and an 8 GiB reserve is
  visibly overcommitted even if it fits the audit's 24 GiB inference budget.
- The Job Pod is described as what it is: sandbox memory inside the runtime,
  not additional host model memory.
- A sensible 12B-class persona set on the same host fits both budgets.
- The implementation is in `internal/ui.budgetFor`; tests pin the separation.
