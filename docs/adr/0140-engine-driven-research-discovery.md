# ADR-0140 — Engine-driven research discovery

**Status:** accepted · **Date:** 2026-11-17 · **Refs:** [ADR-0019](0019-gateway-tools.md),
[ADR-0134](0134-project-cycles-and-blocks.md), [ADR-0139](0139-two-level-network-policy.md)

## Context

`search_web` existed as a registered, envelope-gated tool, but the engine never
called it. The research sub-step fetched only the URLs a human had written into
the task description or project goal. Two consequences:

- a research block could not discover a source the operator did not already
  know;
- the Daily Brief's typed `research.sources -> fetch.urls` edge was decorative
  — the fetch block re-read the project sources, not what research found.

A third issue was cost: `researchContext` ran on **every** task in a
URL-bearing project, including synthesize/verify/retain, so a seven-block
cycle could refetch the same eight URLs seven times.

## Decision

1. **Gate research on the task's tool grant.** Only a task whose
   `AllowedTools` includes `fetch_url` runs the sub-step. The block compiler
   already writes that grant; synthesize/verify/retain do not carry it.

2. **Queries come from data.** A research block may carry `queries` in its
   config. The compiler emits a `SEARCH QUERIES:` list into the description;
   the engine reads at most three. When a research task has `search_web`
   granted, no explicit queries, and a configured search engine, it searches
   its title + project goal once. No topic is hardcoded in Go.

3. **The engine runs the query.** `ToolRunner.SearchWeb` joins the phase port;
   the Core executes it through the gateway exactly like `fetch_url`. A
   search failure (including an unconfigured engine) is a soft-fail with a
   `research_search` audit row, never a job failure.

4. **Result URLs are just URLs.** Discovered hits are deduped with explicit
   URLs, capped at the existing eight-source budget, and fetched through the
   same `fetch_url` policy. A search-engine allow entry does not allow the
   result hosts; each result host needs the global allowlist or a project
   grant (ADR-0139).

5. **The typed edge has a data plane for research.** The upstream block's
   accepted output is bounded and scanned for URLs, so `research -> fetch`
   actually hands over what research found. A general typed-port data plane is
   still out of scope; this is the one edge the product needs now.

## Consequences

- A research block with the search profile can find and cite sources without
  a hand-maintained URL list.
- A cycle no longer refetches the same sources for non-research blocks.
- Discovery is bounded (3 queries, 8 sources, one soft-fail per query), not a
  crawler.
- The audit log distinguishes discovery (`research_search`,
  `research_source`) from fetching (`research_fetch`).
