# ADR-0141 — A coverage ledger for recurring cycles

**Status:** accepted · **Date:** 2026-11-17 · **Refs:** [ADR-0134](0134-project-cycles-and-blocks.md),
[ADR-0139](0139-two-level-network-policy.md), [ADR-0140](0140-engine-driven-research-discovery.md)

## Context

The news template asks the model to separate `New` from `Previously reported`,
but production had no memory of what was previously reported. The retain block
wrote a document; nothing read it. The no-repeat contract existed only in the
benchmark harness, which injected the known old ids into the task.

That made a daily cycle model-dependent: a well-behaved prompt usually worked,
but a repeat was not a deterministic failure.

## Decision

1. **A structured ledger.** `coverage_items` (migration 0052) keys
   `(project, namespace, item_key)` and records title, sources, first/last seen.
   A namespace separates independent ledgers inside one project.

2. **The retain block writes it.** A retain block configured with
   `coverage_namespace` gets a compile-time instruction to end its artifact with

   ```coverage
   {"items":[{"id":"<id>","title":"<title>"}]}
   ```

   and a `COVERAGE NAMESPACE: <ns>` marker. The engine's terminal hook parses
   the accepted artifact and upserts the items. The write is idempotent; a
   failure is a warning and the cycle still completes.

3. **The next compile reads it.** `firstuse.StartDraft` reads the recent keys
   for a synthesize block's `coverage_namespace` and adds them to
   `forbidden_ids_in_sections` under `New`. The block may declare its own
   checks; the dynamic rule is additive.

4. **It is a check, not a prompt hope.** A repeat under `New` fails the
   deterministic verifier; the repair loop can fix it. The model still gets
   the human-readable instruction, but the check is what enforces it.

## Consequences

- A second run no longer presents an old story as new, even when the model
  would prefer to.
- The ledger is per project and namespace; a topic brief does not suppress the
  daily wire.
- A malformed or missing coverage block degrades to the previous behavior
  (no suppression) rather than failing the cycle.
- The benchmark's `new_items_section.old_ids` contract now has a production
  counterpart.
