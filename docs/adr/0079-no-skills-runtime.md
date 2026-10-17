# ADR 0079 — Built-in tools and adaptability, not a skills runtime

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
ARCHITECTURE §25 (tool interface), §26 (skills and extensibility), §21.3
(airlock); [ADR-0064](0064-cognitive-operations.md) (operations),
[ADR-0067](0067-technique-guidance-loop.md) (technique guidance),
[ADR-0074](0074-attempt-escalation.md) (escalation),
[ADR-0076](0076-continuous-evidence.md) (continuous evidence);
`docs/1.0.0-direction.md`.

## Context

M6 sketched a **skills runtime** (user-authored executable skill objects) and
`skill_refinement`; both were deferred. A generic plugin/MCP/tool ecosystem was
a candidate for 1.x. Two reasons now close that direction rather than defer it:

1. **Security.** Every executable extension is a malware/adversarial vector and
   a new trust boundary — sandbox, permission model, supply chain, versioned API.
   A single-operator local system with a **closed tool envelope** is safer
   without one.
2. **The product bet.** What makes Athanor valuable is the **integrated,
   hard-coded system** — the MCE, selection/escalation, corrections, grounded
   outcomes, and the built-in tools. A user-authored extension layer is neither
   where the value is nor a portability goal.

"Hyper-customization" is still wanted — but through configuration and
adaptation, not code. And users can always paste text; that cannot be prevented,
so it must be treated as **data**, never as a capability.

## Decision

1. **No user-authored skills runtime, no `skill_refinement`, no plugin API, and
   no generic MCP/tool ecosystem in the 1.0 line.** This is a direction
   decision, not a temporary deferral.
2. **Customization is built-in and declarative.** The surfaces are
   configuration, personas, prompt/context policy, corrections,
   selection/escalation, and the closed built-in tool set. Hyper-customization
   means tuning those and letting evidence-driven adaptation choose among them —
   not adding executable code to the system.
3. **Pasted text is data.** A "skill" or tool description pasted into a prompt,
   an upload, or an ingestion path is untrusted input: it is scanned by the
   airlock/scanner, **never executed**, and it **cannot grant a tool, permission,
   or capability**. It may be analysed for intent, but any analysis is advisory
   to the operator.
4. **Allowed but not encouraged or advertised.** We cannot stop a user pasting
   text, and we do not build a path that turns it into an extension mechanism.
   It is not documented as one.
5. **No lock-in, no portability goal.** Athanor's value is its hard-coded,
   integrated behaviour. Standardizing concepts so others can build something
   similar is a *maybe later*, not a commitment, and it does not require an
   extension layer here.
6. **Revisit only on a concrete integration need** that a built-in tool cannot
   meet; then add a specific built-in tool, not a generic plugin surface.

## Consequences

- A smaller attack surface: no plugin sandbox, no third-party skill trust model,
  no versioned skill API, no supply chain to police.
- Less open-ended extensibility; the answer to "can I add X?" is "we add X as a
  built-in tool," decided by the operator.
- The documentation must make **configuration + adaptation** the customization
  path, and must not present pasted text as an extension mechanism.
- Pasted text stays inside the scanner/airlock boundary; it is a prompt-injection
  surface to defend, not a feature to support.
- The tool envelope stays closed, so Gate G2's structural proof stays simple.

## Alternatives rejected

- **Skills runtime (deferred from M6).** Adds a trust boundary and an API to
  maintain; the built-in repertoire is the better lever, and executable
  extensions are exactly the vector we want to discourage.
- **MCP/plugin ecosystem.** The same cost, plus a broad external contract, for a
  single-operator local system.
- **Do nothing / no position.** Leaves an open question that keeps resurfacing
  in scope discussions; a direction decision closes it.
