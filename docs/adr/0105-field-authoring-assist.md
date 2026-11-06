# ADR 0105 — Field authoring assist is a contract, not a magic rewrite

**Status:** Accepted — first implementation slice · **Date:** 2026-11-05 ·
**Refs:** [ADR-0094](0094-project-discovery-portfolio.md),
[ADR-0097](0097-ai-plan-candidates.md),
[ADR-0104](0104-composition-space-not-personas.md),
ARCHITECTURE §27.

## Context

Most of Athanor's inputs are prompts in disguise: a project goal, a task goal,
acceptance criteria, discovery constraints, interview answers, correction
feedback, and operator notes. The user should not have to know what the
downstream phase needs from each field. A generic “make this better” button is
tempting, but it would invent requirements, homogenize voice, and silently
change scope.

The user asked for a visible, reversible control: a wand beside the field that
refines the current text for that field, and a Revert button that restores the
original.

## Decision

1. **The control is explicit and reversible.** A wand button refines the
   current text; the original is retained in the browser and Revert restores it.
   Nothing is applied without the user seeing the replacement. The page still
   works without JavaScript.
2. **Each field has a contract.** `internal/assist` owns a `Spec` per field:
   purpose, downstream consumers, requirements, what must be preserved, and a
   length target. The model prompt is built from that contract, not from a
   generic template.
3. **The result is structured.** The model returns
   `{text, notes, added_assumptions}`. Anything new that is not strictly
   implied belongs in `added_assumptions`, never smuggled into the field.
4. **Facts are preserved.** The contract forbids inventing facts, URLs, names,
   numbers, requirements, constraints, file paths, or commands. Sources are not
   eligible for the wand in this slice because inventing a citation is worse
   than a rough note.
5. **Modes are contracts.** Improve is the default; proofread changes grammar
   only; expand must flag additions; shorten must preserve constraints. The UI
   exposes the default and can pass a mode per field.
6. **The model is local and measured.** The assist uses the measured pool,
   preferring quality over latency and falling back to the tall persona. It is
   one bounded, tool-free call with a JSON response and a hard output cap.
7. **The user's edit is the signal.** A generated suggestion is not training
   data until the user accepts or edits it and the downstream job produces a
   grounded outcome. Accepted rewrites are a candidate fine-tuning set for a
   small local editor model, not a self-training loop.

## Consequences

- The first slice covers project goal, project/task acceptance criteria,
  discovery brief fields, interview answers, correction reason/desired
  behavior, job/interruption notes, and review notes.
- The feature is intentionally not on every input: model names, numbers,
  sources, and structured controls are excluded.
- A failed or empty refinement leaves the field untouched and shows the error.
- The contract is reviewable and versionable; changing a field's purpose is a
  code change, not a hidden prompt drift.
- The wand is an authoring aid, not a HITL gate: internal, reversible text
  changes proceed under the normal user edit, while external/irreversible
  actions still require approval.

## Open questions

- Should accepted rewrites be attributed to the editor model in the strategy
  outcome, or only recorded as preference data?
- Should there be an explicit “quality vs speed” toggle, or is quality the
  right default for an interactive action?
- How much of the field contract should be user-editable in advanced settings?
