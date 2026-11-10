# ADR 0134 — Projects are timelines of cycles, cycles are typed block graphs

**Status:** Accepted · **Date:** 2026-11-10 ·
**Refs:** [ADR-0079](0079-no-skills-runtime.md),
[ADR-0093](0093-project-capabilities-not-archetypes.md),
[ADR-0131](0131-engine-package-layering.md),
[ADR-0132](0132-project-intake-one-intent.md), ARCHITECTURE §5–§9.

## Context

The project model is one goal, one plan, one DAG. That shape cannot express
the work the product is actually for:

- A game project needs a discovery cycle (research, three concepts, human
  choice), a design cycle (spec, critique), and a build cycle (implement,
  test, review). Today those would be three unrelated projects.
- A daily brief needs the same graph to run again tomorrow, with memory of
  what was already seen. Today a project is a one-shot plan.
- A user who does not know what to build needs Athanor to research and
  propose, not a form that asks them to invent fields. Today onboarding is a
  form-first intake state machine.

The substrate already has the execution primitives: `internal/dag` (graph),
`internal/scheduler` (dependencies, retries, failure policies),
`internal/engine` (phases), `internal/hitl`/`internal/review` (gates),
`internal/gateway` (allowlisted fetch), and the MCE (memory). `Task.TaskType`
already declares `one_time | recurring | triggered`; execution of the latter
two is backlog.

## Decision

### 1. Project → cycles → blocks

A **project** is the ongoing container: outcome, capabilities, repository,
sources, memory, decisions, and a timeline. A **cycle** is one bounded run
with a goal, a graph, a budget, a trigger, and a review policy. Cycle kinds
(`discovery`, `design`, `build`, `operate`, `review`) are not project kinds.

A **block** is one operation from a closed repertoire (`research`, `fetch`,
`extract`, `condense`, `generate_options`, `critique`, `select`, `synthesize`,
`implement`, `test`, `verify`, `retain`, `review`, `publish`). Blocks have
typed ports. The model composes blocks; it never defines them.

A **template** is a versioned, parameterized cycle graph with triggers. A
**run** is a cycle; it records the template version and trigger that produced
it. No separate run table.

### 2. Compile, do not fork the executor

`internal/workflow` validates the graph and compiles it to
`project.TaskSpec` values; the existing `project.CreateDAG` and
`internal/scheduler` execute them. `internal/cycle` owns the timeline and the
`tasks.cycle_id` link. A second executor would fork the state machine and the
crash-recovery contract, so there is none.

### 3. Onboarding is cycle 0, not a form

Guided creation is a conversation with Athanor as **architect**, not operator.
It asks only the questions that change the plan, may research when asked, and
produces a reviewed **Project Frame** plus a **Cycle 0 graph**. A vague goal
makes cycle 0 a discovery cycle: research → options → human select → frame.
A precise goal makes it a short design/build cycle. The user approves the
frame and graph; low-risk internal work proceeds, external or irreversible
actions stay behind HITL.

The existing structured intake API remains the advanced/CLI surface. The
default UI path is conversational.

### 4. The canvas is a contract editor

The graph is data, not a visual programming language. A basic full canvas
(pan/zoom, typed nodes and edges, palette, inspector, validation, run status,
diff/version, undo) edits the template. It does not accept user code,
arbitrary expressions, or unknown block kinds. The first UI ships with the
closed repertoire; a marketplace and subgraph authoring are later.

### 5. Legacy one-shot projects are cycle 0

Migration 0048 creates one `legacy` cycle per existing project and backfills
`tasks.cycle_id`. A project with no cycles behaves exactly as before. New
tasks may carry a cycle ID; empty means a legacy one-shot task.

### 6. Execution contracts and data flow (Phase 2)

Blocks are only useful if a downstream block sees what its upstream blocks
produced and each block tells the engine what output counts as done.

- **Contracts.** Each kind carries an instruction and default acceptance
  criteria. `Compile` folds them into the task description and criteria. An
  operator instruction is appended, never substituted, so a block's meaning
  cannot be erased by a task note.
- **Data flow.** The context assembler appends the accepted artifact of each
  completed dependency task to the task description, bounded per block and in
  total. The task description is the pinned task section of every prompt, so
  this is the one data channel and no second executor or prompt schema is
  needed.
- **Memory.** Completed jobs are already remembered per project (ADR-0114) and
  retrieved per prompt. `retain` is the explicit curation block: its task
  records what was covered, so the next operating run starts from that record
  instead of repeating it.
- **Templates.** `daily-brief`, `discovery`, and `build` are ordinary graphs,
  not privileged flows. Creating a cycle from one records the template ID and
  version on the cycle, then opens the draft in the canvas. Triggers are data
  until the trigger runner lands.
- **Completion.** A goal that reaches `completed` or `failed` marks its cycle
  terminal through a scheduler seam; the UI no longer shows a finished run as
  `running`.

## Consequences

- One project can contain design and coding work without archetype forks.
- Recurring work and one-shot work share one scheduler, one budget model, and
  one audit trail.
- The block repertoire is the safety boundary: adding a kind is a product
  decision, not a model output.
- The canvas becomes the authoring surface, but the graph schema lands first;
  no canvas is built against a moving model.
- The model can decide more during onboarding without silently inventing
  constraints, because the frame is an artifact the user approves.
- The v4 benchmark can measure cycles, block selection, recurrence, memory,
  and real utility instead of only single-shot artifacts.

## Alternatives rejected

- **More project archetypes.** They cannot express a project that changes
  shape across cycles, and ADR-0093 already replaced archetypes with
  capabilities.
- **A separate workflow engine.** It would duplicate scheduling, recovery,
  budgets, and audit.
- **A free-form node language.** It would turn a contained agent system into
  a code-execution platform and break Gate G1's posture.
- **Field-first onboarding.** It asks users to guess taxonomy and settings
  the system can infer, and it cannot handle “I don’t know what to make.”
- **A canvas before the schema.** It would freeze UI assumptions before the
  block semantics and compile contract are testable.
