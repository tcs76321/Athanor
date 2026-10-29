# ADR 0085 — Compacted project memory is retrieved into tier 5

**Status:** Accepted · **Date:** 2026-10-28 · **Refs:**
ARCHITECTURE §10.2 (context gradient), §10.3 (compaction), §11.2 (assembly),
§17.1 (memory consolidation), §25 (`query_memory`);
[ADR-0025](0025-compaction.md) (compaction), [ADR-0026](0026-memory-retrieval.md)
(hybrid retrieval), [ADR-0038](0038-feedback-injection.md) (the analogous
correction seam), [ADR-0083](0083-repository-working-set-pack.md) (chunk bodies).

## Context

The MCE has a hybrid retriever (`mce.Retriever`: FTS5 bm25 fused with optional
vector cosine by reciprocal-rank fusion) and a deterministic compaction store.
The only caller of the retriever was the `query_memory` internal route. The
model cannot invoke tools (ADR-0080), and the engine never called the route, so
compacted episodic memory was written by daydream consolidation and then never
read by a generation prompt. The MCE was "functional" in storage and tests but
inert in the loop.

The correction seam (ADR-0038) already solved the analogous problem: a
project-scoped, severity-ranked retrieval result is rendered into a prompt tier.
Memory should use the same shape, not a new HTTP path.

## Decision

1. **Add an engine `MemorySource` seam** returning `mce.MemoryHit`s for a
   project and query. The production adapter lives in `cmd/` over the MCE
   retriever; `internal/engine` does not import the store.
2. **Render memo hits in tier 5 (episodic).** Chunk hits are not rendered here:
   their metadata already reaches tier 6 and their bodies reach tier 3 through
   the working-set pack. A memo is rendered as one `[memory] …` line.
3. **Default on, bounded, and auditable.** `context_engine.memory_in_prompt`
   defaults true; `memory_search_top_k` (default 8) caps the query. Every
   injection writes a `memory_injected` event with project, hit count, bytes,
   and memory IDs.
4. **No model call and no tool call.** Retrieval is the same deterministic
   hybrid query used by `query_memory`; the engine is a caller, not the model.
5. **The prompt stays honest.** An empty result leaves tier 5 absent, so a
   fresh project's prompt is byte-identical to the pre-P1 prompt.

## Consequences

- Compaction now has a consumer: a daydream-consolidated memo can influence the
  next job, closing the "write-only memory" gap.
- Tier 5 is evictable (the 7→6→5→4 ladder), so memory pressure cannot breach
  the pinned tiers. A large memo is not truncated; the retriever's top-K and the
  ladder decide.
- Retrieval quality is now a prompt-quality lever, and a bad memo can mislead a
  job. The audit row and `memory_in_prompt: false` are the operator controls;
  the benchmark must measure retrieval hit and pass rate, not assume benefit.
- The `query_memory` route remains the pod-facing/test surface; the engine uses
  the seam directly, avoiding a loopback HTTP call per prompt.

## Alternatives rejected

- **Call the `query_memory` route from the engine.** Adds a loopback request
  and couples prompt assembly to the HTTP surface for no capability gain.
- **Render chunk bodies in tier 5.** Duplicates tier 3 and bypasses the
  working-set caps; chunk content belongs in the pinned pack.
- **Leave memory write-only.** The MCE's compaction and retrieval costs are
  already paid; not reading them makes the subsystem decorative.
- **Use the LLM to summarize memory per prompt.** Non-deterministic, costly,
  and violates the Temp-0.0 compaction invariant.
