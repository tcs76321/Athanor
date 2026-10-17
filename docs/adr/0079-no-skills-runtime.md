# ADR 0079 — External skills are unnecessary by design

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
ARCHITECTURE §25 (tool interface), §26 (skills and extensibility), §21.3
(airlock); [ADR-0064](0064-cognitive-operations.md) (operations),
[ADR-0067](0067-technique-guidance-loop.md) (technique guidance),
[ADR-0074](0074-attempt-escalation.md) (escalation),
[ADR-0076](0076-continuous-evidence.md) (continuous evidence);
`docs/1.0.0-direction.md`.

## Context

M6 sketched an external **skills runtime** (user-authored executable skill
objects) and `skill_refinement`; both were deferred. A generic plugin/MCP/tool
ecosystem was a candidate for 1.x.

Athanor's design already covers what external skills are usually for:

| Skill need | Internal mechanism |
|---|---|
| Behaviour / instructions | personas, prompt/context policy, CorrectionRecords |
| Domain knowledge | project repository indexing, the MCE, uploaded documents |
| New capabilities | the closed built-in tool set |
| Reusable workflows | DAG decomposition + scheduler + attempt escalation |
| Getting better at a class of work | grounded outcomes + selection + the benchmark |

An external skill layer would duplicate those with **less attribution** (its
behaviour is not measured against the frozen benchmark), **more risk** (an
executable vector and a new trust boundary), and it would **compete with** the
mechanisms that are supposed to improve the system. That is why the position is
not "we haven't got around to it" but **"we don't want it."**

## Decision

1. **External skills and plugins are unnecessary and likely counterproductive
   for using Athanor as designed.** They are not a supported direction and not a
   deferred feature.
2. **The customization path is internal and measured**: configuration, personas,
   prompt/context policy, CorrectionRecords, project context/MCE, the
   selection/escalation layer, and the built-in tool set. Domain instructions go
   through corrections/config/context — which the benchmark can measure — not a
   separate skill artifact.
3. **A missing capability becomes a built-in tool**, decided by the operator
   (with its containment and tests). It does not become a user extension.
4. **The internal tool interface is closed.** The §25 tool envelope and the
   internal API are an internal contract (tools, schemas, permissions) — **not**
   an external plugin protocol, and not marketed as MCP.
5. **Untrusted input is data.** Content that arrives through a prompt, upload, or
   ingestion path is scanned by the airlock/scanner, is never executed, and
   cannot grant a tool, permission, or capability. There is no supported path
   that turns input into executable behaviour.
6. **No documented extension mechanism.** The documentation presents
   configuration + adaptation as customization; it does not describe a plugin or
   skill path.

## Consequences

- A smaller attack surface: no plugin sandbox, no third-party trust model, no
  supply chain, no versioned skill API.
- Behaviour that improves Athanor is **measurable**: it lives in mechanisms the
  benchmark can exercise, not in unmeasured user artifacts.
- Less open-ended extensibility; the answer to "can I add X?" is "we add X as a
  built-in tool," decided by the operator.
- The customisation story is "tune the system and let evidence choose," not
  "write a skill."

## Alternatives rejected

- **Skills runtime (deferred from M6).** Duplicates the internal mechanisms with
  less attribution and an executable vector.
- **MCP / plugin ecosystem.** The same cost plus a broad external contract, for
  a single-operator local system.
- **Do nothing / no position.** Leaves an open question that keeps resurfacing;
  a direction decision closes it.
