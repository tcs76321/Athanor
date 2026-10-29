# ADR 0083 — The repository working set is a byte-capped chunk pack

**Status:** Accepted · **Date:** 2026-10-28 · **Refs:**
ARCHITECTURE §10.1 (lossless division), §10.5 (tiers), §11.2 (prompt assembly),
§12.6 (context floors), §25 (tools);
[ADR-0059](0059-automatic-working-set.md) (Dormant Index union),
[ADR-0080](0080-engine-executed-tools.md) (no model-invoked tools),
[ADR-0082](0082-repository-working-set.md) (staged read-only snapshot);
`docs/probes/bench-v2-findings.md` (X-code 0/6).

## Context

The v2 benchmark's H tier is strong (92%) and X code is 0/6. The diagnostic
traces show a consistent shape: the model receives the task, the acceptance
criteria, the active chunk, and a Dormant Index of file *names and summaries* —
but not the bodies of the repository files it is asked to change. It cannot
call `context_swap` (ADR-0080), and tier-3 seeding is opt-in. The benchmark's
X-code tasks are multi-file and repository-scale; a table of contents is not
enough context to edit them correctly.

The data tasks exposed the same gap differently: the JSON Schema lived in a
dormant chunk, so every candidate failed `output is not valid JSON`. That
specific case is now fixed by putting the schema in tier 2 (ADR-0070
amendment). The general case remains: code jobs need a bounded view of the
relevant files.

## Decision

1. **Add an optional `WorkingSet` seam** to the engine's context provider. It
   returns the top-ranked **project** chunks, already byte-capped. Chunk bodies
   are full-fidelity; the pack is never summarized or truncated.
2. **Render the pack in tier 3**, after the active chunk, as
   `REPOSITORY WORKING SET` with per-file path/line/kind headers. The assembler
   dedupes by chunk ID and preserves provider order, so the prompt remains
   deterministic.
3. **The pack is pinned but hard-capped.** `working_set_max_bytes` (default
   32 KiB) and `working_set_max_chunks` (default 8) are validated; a chunk that
   does not fit is skipped, never sliced. The active chunk is excluded.
4. **`working_set_mode` is `auto` by default:** code jobs get the pack, other
   archetypes do not. `on` enables it everywhere, `off` disables it. Operators
   can restrict it to phases; the default is planning, diverging, reflecting,
   and synthesizing — evaluation/comparison work on a candidate, and
   self-refine works on a failing candidate.
5. **Retrieval is deterministic and model-free.** The adapter uses the existing
   FTS5 bm25 retriever (plus the optional vector half) over the project-scoped
   Dormant Index; before summaries are ready it falls back to the most recently
   updated project chunks. No model call is made to build the pack.
6. **Repository text is data, not policy.** The section says so explicitly; the
   pack never grants a tool or changes the §25 envelope.
7. **The pack is audited** as `working_set_sourced` with phase, chunk count,
   bytes, and chunk IDs, so a post-mortem can tell "the file was not retrieved"
   from "the model ignored it."

## Amendment (P1, 2026-10-28) — the engine drives the active chunk

The pack gives the model many chunk bodies, but the §10.1 active/dormant pointer
was still only reachable through `seed_active_chunk` or the unused
`context_swap` route. For a code job with the pack enabled, the engine now asks
the provider to `SeedActive`: it ranks the same project chunks, takes the first
that fits `seed_active_max_bytes`, and calls `ChunkStore.Swap`, persisting the
active pointer byte-exactly. The prompt then renders that chunk as the active
chunk and the remaining pack after it. `active_chunk_swapped` records the chunk
and path. The `context_swap` route remains for the benchmark/test surface; it is
no longer the only way the swap mechanism runs.

## Consequences

- A code job now sees the bodies of the most relevant files before it plans or
  generates, without a model-invoked tool loop and without changing the
  containment posture.
- The prompt is larger for code jobs. The hard cap and the §10.4 pressure gate
  are load-bearing: a pack that would breach the persona window pauses the job
  rather than silently truncating.
- The pack is a retrieval artifact: a bad ranking can crowd out a better file.
  The chunk count, byte cap, and `context_engine.working_set_phases` are the
  operator controls; the benchmark must measure the change, not assume it.
- Tier 3 stays "never evicted"; the pack must fit or be reduced at retrieval
  time. It does not participate in the 7→6→5→4 ladder.
- `working_set_mode: auto` is a behavior change for code jobs. It is gated by
  the 0.0.0 capability measurement; `off` restores the pre-P0 prompt.

## Alternatives rejected

- **Model-invoked `read_file`/`search`.** The larger capability jump, but it
  reverses ADR-0080 and needs a protocol, loop budgets, and a new containment
  review. The pack is the smaller first step; the bounded read-only recon loop
  remains the conditional P0-1B fallback.
- **Seed only the single active chunk.** One chunk is not enough for repo-scale
  edits, and it makes every job depend on the top-1 ranking.
- **Summarize the repository into the prompt.** Loses exact identifiers and
  violates the lossless-over-lossy principle for code.
- **Let the pack be evicted when the prompt is full.** It is pinned; the
  correct response to pressure is a smaller retrieval cap or a floor violation,
  not silently dropping the working set.
