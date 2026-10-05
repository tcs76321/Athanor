# ADR 0048 — Reduce & complete: feedback→policy, reflection gating, MCE scope (F4-T7)

**Status:** Accepted — lands with F4-T7 · **Date:** 2026-10-05 · **Refs:**
ARCHITECTURE §10.1, §10.5, §13.4, §18; ROADMAP F4; [docs/f4-plan.md](../f4-plan.md);
[ADR-0044](0044-compute-policy-seam.md)

## Context

F4-T7 is the "reduce and complete" wave. Two threads:

1. **Finish the feedback→policy channels.** M6-T11 mines insights and injects
   their *statements* into prompts, but active insights did not yet affect
   the persona plan — the "loop learns" arm of Gate G-F4.
2. **The MCE working-set gap (M3-T7 finding A).** Repository indexing
   attributes chunks to the **project** (`internal/mce/index.go`), but the
   engine's context provider queries the Dormant Index by **job**
   (`ContextProvider.DormantIndex` → `ChunkStore.IndexForJob`). No production
   path ingests repository chunks with a job ID, so the automatic working set
   (tiers 3 and 6) is empty for normal jobs; repository context is reachable
   only through `query_memory` + `context_swap`.

## Decision

**Feedback→policy (T7a).** An *active* winning insight whose pattern is
`diverging.persona=<persona>` (optionally scoped to `archetype=<a>`) now leads
`Plan.DivergenceRoles` for matching jobs, and a
`policy_biased_from_insight` audit row records the application. Proposed,
muted, and retired insights are excluded by the source itself, so the §13.4
inertness contract is preserved. Template ranking and ExplorationPath
proposals remain future channels (they are greenfield; the gate needs only the
persona-plan bias).

**Reflection gating (T7b).** A cycle in which every candidate failed on a
`security_issues` finding is not a content problem regeneration can fix. The
engine now fails fast (`reflection_skipped_security`) instead of spending
another divergence + reflection cycle. Test/structural failures still reflect
as before — a deterministic verifier failure is exactly what a regenerate is
for.

**MCE working-set scope (T7c).** The automatic working set stays scoped to the
**tool path** (`query_memory` + `context_swap`) rather than injecting
project-scoped repository chunks into the Dormant Index wholesale. Rationale:
repository indexes are large and unbounded per project; dumping one into every
job's prompt would bloat the working set far worse than the current empty
tier, and the probe's token data (which would size a relevance/limit policy)
is not yet in hand. This is a conscious scope, not an oversight: the tools
remain the retrieval path, and the ADR/ARCHITECTURE note is updated to say so.
A relevance-ranked, size-bounded injection is a future task once the probe
measures working-set token pressure.

## Consequences

- The Gate G-F4 "the loop learns" arm is satisfiable and tested: a mined
  insight demonstrably changes a subsequent plan with an audit row.
- Security failures no longer masquerade as iterative content failures.
- The MCE tier-3/tier-6 behavior is documented honestly; no silent claim that
  project context is auto-injected.
- Reducing the §10.5 KV tier ladder is deferred, not done: the probe's token
  data (step after this ADR) decides whether context pressure justifies it.

## Alternatives rejected

- **Inject the whole project index.** Unbounded prompt growth; would regress
  the very context floors the MCE exists to protect.
- **Reflect on security failures.** A model told to try again on a security
  violation is being asked to work around a guardrail; fail fast is safer.
- **Wire a relevance policy without data.** The probe's token measurements
  are the input that sizing requires; doing it blind would be a guess.

## Not in scope

- Template ranking and ExplorationPath proposals (future channels).
- A relevance-ranked working-set injection (needs probe token data).
- Removing the §10.5 ladder (revisit with probe data).
