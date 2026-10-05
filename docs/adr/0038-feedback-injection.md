# ADR 0038 — Feedback injection into prompt assembly (M6-T7)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §11.2 §8,
§10.5 tier 4, §18.3; ROADMAP M6-T7; ADR-0023 (context assembly priority),
ADR-0037 (CorrectionRecords).

## Context

M6-T6 ([ADR-0037](0037-correction-records.md)) captures `CorrectionRecord`s
but nothing reads them. The §11.2 assembly already reserves position 8 /
§10.5 tier 4 (`prompt.Input.Corrections`, `TierCorrections`), and the
eviction ladder already treats tier 4 as the *last* evictable tier — but no
producer fills it, so every prompt is correction-free.

§18.3 fixes the ranking: high severity before low, project scope before
global, retrieved by similarity to the task, summarized at Temp 0.0, and
mutable (mute/edit/promote).

## Decision

### 1. `Relevant` is the retrieval seam, ranked deterministically

`corrections.Repo.Relevant(ctx, projectID, limit)` returns active records
that are global or project-scoped to the project, ordered:

```text
severity (critical → low) then scope (project → global) then recency
```

capped at `limit`. The severity-first order makes a high-severity project
correction outrank every low-severity one, which is the acceptance bar. The
method is a seam: §18.3's vector-similarity retrieval is **deferred** —
corrections are not in the MCE vector index yet — and a similarity ranker
will refine `Relevant` without touching the assembler.

### 2. The engine feeds the existing tier; the ladder still rules

`Engine.SetCorrectionSource` wires a `CorrectionSource` (satisfied by
`*corrections.Repo`). `promptTiersFor` calls `Relevant(task.ProjectID,
maxInjectedCorrections)` (a small fixed cap; token pressure is handled by the
§10.5 ladder, not the cap) and renders each record as one line
(`[category/severity scope] derived rule`). The assembler places them at
§11.2 position 8, priced as `TierCorrections`; the ladder may evict the tier
last under pressure. A nil source leaves the tier empty and every prompt
byte-identical to pre-M6-T7.

### 3. Injection is audited with token accounting

For every call that gathered corrections, the engine appends a `feedback`
event `corrections_injected` (phase, ids, count, tier tokens, and whether the
ladder suppressed the tier). When the tier survived, each record's
`applied_count` is incremented. The per-section token accounting already in
`llm_call` / `kv_cache_assembled` includes the corrections section, so a
post-mortem can read both the position and its price.

### 4. Edit joins mute/promote

`Repo.Update` applies partial edits to a record's category/severity/scope/
feedback/rule (validating the closed sets), and the existing `PATCH
/corrections/{id}` accepts those fields alongside `status`. This completes
§18.3's mute/edit/promote surface; delete is left to the UI task (M6-T8b).

## Consequences

- A project's earlier rejections now shape its later prompts, highest
  severity first, and the effect is auditable and measurable.
- Injection is bounded twice: a count cap at retrieval and the §10.5 token
  ladder at assembly.
- Similarity ranking is a documented gap, isolatable to `Relevant`.
- The engine grows one nil-safe seam; no new dependency, and Gate G1 is
  unaffected.

## Implemented (M6-T7.2–T7.4)

`corrections.Repo.Relevant` and `Update`; the engine `CorrectionSource` seam,
corrections rendered into `prompt.Input.Corrections`, the
`corrections_injected` audit row, and `MarkApplied` on injection; the PATCH
route extended with edit fields. Tests cover the severity/scope ordering, the
cap, the audit row, and the applied count.
