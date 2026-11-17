# ADR-0143 — Operator workbench and global allowlist write-back

**Status:** accepted · **Date:** 2026-11-17 · **Refs:** [ADR-0139](0139-two-level-network-policy.md),
[ADR-0141](0141-coverage-ledger.md)

## Context

Research and daily-news work was spread across the project page, the canvas,
the artifact reader, the review queue, and the raw event trace. An operator
checking "what did today's run produce, what fed it, what is already covered,
and when does it run next?" had to visit several pages and mentally join them.

Adding a source still meant editing `network.allow_list` and restarting the
Core. Project grants (ADR-0139) removed that for research sources, but the
global ceiling had no in-product write path.

## Decision

1. **One workbench page.** `GET /ui/projects/{id}/workbench` shows the latest
   accepted artifacts, declared sources with allowlist status, project egress
   grants (with revoke), the coverage ledger by namespace, and scheduled
   triggers with timezone and next run. It links to the reader, download, and
   canvas; it does not duplicate them.

2. **Grants are managed where they are used.** Revoking a grant is a
   project-scoped POST; a grant id from another project is a 404. The workbench
   is the place to see why a source was allowed.

3. **The global allowlist can be written from the UI.** A disallowed source
   offers "Add to global allowlist", which rewrites `network.allow_list` through
   `config.UpdateNetworkAllowlist` (comment-preserving, validated, atomic) and
   marks a restart required. The running gateway keeps its old policy until the
   Core restarts; the banner says so. The UI never silently widens policy.

4. **Project grants stay the default path.** The global write-back is the
   ceiling change; the per-project grant is the ordinary research path. The
   workbench shows both.

## Consequences

- The daily loop is one page: read the report, check sources, check coverage,
  check the next run.
- A global allowlist change is still an explicit operator action with a
  restart, not a side effect of a fetch.
- The page is read-mostly and uses the existing repositories; no new service.
