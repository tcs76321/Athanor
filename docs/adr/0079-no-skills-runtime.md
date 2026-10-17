# ADR 0079 — Built-in tools and adaptability, not a skills runtime

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
ARCHITECTURE §25 (tool interface), §26 (skills and extensibility);
[ADR-0064](0064-cognitive-operations.md) (operations),
[ADR-0067](0067-technique-guidance-loop.md) (technique guidance),
[ADR-0074](0074-attempt-escalation.md) (escalation),
[ADR-0076](0076-continuous-evidence.md) (continuous evidence);
`docs/1.0.0-direction.md`.

## Context

M6 sketched a **skills runtime** (user-authored skill objects) and
`skill_refinement`; both were deferred. A generic plugin/MCP/tool ecosystem was
a candidate for 1.x. The product bet is now clearer: what makes Athanor useful
is **built-in tools plus internal adaptability** — selection, escalation,
context/memory, corrections, and grounded outcomes — not a user-authored
extension format. A generic plugin surface also adds a new trust boundary, a
sandbox, and a long-term maintenance burden for a single-operator local system.

## Decision

1. **No user-authored skills runtime, no `skill_refinement`, and no generic
   plugin/MCP ecosystem in the 1.0 line.** This is a direction decision, not a
   temporary deferral.
2. **Invest in the built-in tool set and adaptability instead**: the closed
   tool envelope (§25), the cognitive-operation repertoire, learned selection,
   escalation, the MCE, corrections, and the human anchor.
3. **Integrations are deliberate, not pluggable.** A concrete integration need
   grows a specific built-in tool (with its own containment and tests), not a
   generic plugin surface. Revisit this ADR only when that need is demonstrated
   and cannot be met by a built-in tool.

## Consequences

- A smaller surface: no plugin sandbox, no third-party skill trust model, no
  versioned skill API to maintain.
- Less open-ended extensibility; the answer to "can I add X?" is "we add X as a
  built-in tool," decided by the operator.
- The tool envelope stays closed, so Gate G2's structural proof stays simple.
- The adaptability bet carries more weight: selection/escalation/MCE must do
  the work a skills layer might have done.

## Alternatives rejected

- **Skills runtime (deferred from M6).** Adds a trust boundary and an API to
  maintain; the built-in repertoire is the better lever today.
- **MCP/plugin ecosystem.** The same cost, plus a broad external contract, for a
  single-operator local system.
- **Do nothing / no position.** Leaves an open question that keeps resurfacing
  in scope discussions; a direction decision closes it.
