# ADR 0106 — Adaptive host reserve floor and recommendation

**Status:** Accepted · **Date:** 2026-11-06 ·
**Refs:** ARCHITECTURE §12.3 (context feasibility), §24 (resources);
[ADR-0099](0099-measured-model-fit.md) (measured fit);
[ADR-0102](0102-separate-host-and-runtime-memory-budgets.md) (host vs runtime).

## Context

The host reserve (`inference.host_reserve_mb`) is memory kept outside any model:
macOS, apps, the Core, and — on macOS — the Podman VM. It was a fixed default
(4 GiB) with a free-form input. On a normal Apple Silicon install the Podman VM
is 8 GiB, so the shipped default was **smaller than the VM it was supposed to
cover**. A first-use operator saw a warning on the Model Pool step telling them
to raise a value whose control lived on an earlier step, with no explanation of
what the number meant or what to set.

The result was an unactionable warning and a model-fit budget that could treat
the VM as free.

## Decision

1. **Derive a floor from the detected runtime.** When a separate runtime VM
   exists (macOS/Podman machine: host memory > runtime memory), the hard floor
   is the VM size. Without a VM the floor is 2 GiB. The recommendation is the
   floor plus 1 GiB (rounded to 256 MB), capped below host RAM.
2. **Clamp, do not trust.** A configured reserve below the floor is not used
   for the budget. The effective reserve is the floor, and the UI states that
   it is enforcing it. This keeps `host_reserve_mb` readable for compatibility
   while making the budget safe.
3. **Allow a deliberate margin below the recommendation, not below the floor.**
   The operator may set the reserve between the floor and the recommendation.
   The UI shows a non-blocking warning (“below recommendation, allowed”) and
   explains the tradeoff: less headroom for macOS/VM, more room for models.
   Saving below the floor is rejected with the exact minimum and recommendation.
4. **Default to auto.** The shipped default is `host_reserve_mb: 0`, meaning
   “resolve from the detected runtime”: the recommendation (VM + 1 GiB on a
   separate VM, 4 GiB without one). A legacy explicit value (for example the old
   4 GiB default) is honored only above the floor; below it, the effective value
   is clamped. The Resources step shows the effective value, the minimum, and
   the recommendation, and offers a one-click “use recommended and restart”.
5. **Re-detect on every render.** The floor and recommendation are derived from
   the live `Hardware` snapshot (host bytes, runtime bytes), so resizing the
   Podman machine or moving to a different host changes the floor without a
   migration. Qualification, auto-assignment, and the pool fit limit use the
   same effective reserve.
6. **Fix on the page that warns.** The Model Pool warning now carries the
   plain-language explanation, the recommended value, a “use recommended and
   restart” action, and a link to Resources. A fix started there returns to the
   Model Pool step after the restart (qualification results are durable).

## Consequences

- A fresh install cannot run with a model budget that ignores the Podman VM.
- The warning is actionable in place and the default is no longer surprising.
- The operator can still trade headroom for model capacity deliberately, but
  cannot set a value below the VM floor through the UI.
- The stored value may be below the effective value until the operator saves;
  the UI is explicit about the difference.
- The host-side model audit keeps its own `-audit-reserve`; aligning it with the
  detected VM floor is a follow-up because the audit runs before the Core knows
  its cgroup-limited runtime size.

## Alternatives rejected

- **Reject below the recommendation.** Too strict for a 32 GiB host running a
  19 GB model; the operator may accept less headroom deliberately.
- **Silently rewrite the config to the recommendation at install.** The operator
  should see and approve a memory budget change.
- **Hardcode 9 GiB.** Wrong for a 6 GiB VM, a 16 GiB VM, or a Linux host without
  a VM.
- **Warn only on Resources.** The Model Pool shows the model-fit consequence;
  the warning has to be fixable there too.
