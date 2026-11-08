# ADR 0131 — Engine package layering: phases, acceptance, context, repair

**Status:** Accepted · **Date:** 2026-11-07 · **Refs:** ADR-0001, ADR-0012,
ADR-0022, ADR-0023, ADR-0045, ADR-0062, ADR-0080, ADR-0117;
`internal/engine`, `internal/gate`, `internal/project`

## Context

`internal/engine` grew to 17.6k lines across 104 Go files (8.4k production,
9.2k tests) with 145 methods on one type. It contains four distinguishable
jobs:

- **phase orchestration** — the seven §13.1 phases and their prompt text;
- **acceptance decisions** — the §19.3 guard, reward-hacking guard, cost tie,
  rubrics, and verdict parsing;
- **context assembly** — the §10.4/§10.5 tier ladder, suppression, eviction,
  and the pre-call context policy;
- **repair policy** — self-refine election, candidate choice, and the
  observation request.

The package is also the highest-risk surface in the system: crash recovery,
acceptance integrity, and every event the benchmark reads flow through it. The
1.0.0 GUI work will add pressure in the same tree, and a single 34-file
package makes review, ownership, and test isolation harder than the code
warrants.

A pure file split would not help: without an enforced dependency direction the
subpackages would call back into the root and recreate the monolith with more
indirection. The split must therefore ship with a structural gate.

## Decision

### 1. Five subpackages under `internal/engine`

| Package | Responsibility | Imports (engine-internal) |
|---|---|---|
| `acceptance` | Pure §19 decisions: `Decide`, reward-hack and cost guards, final record, rubric, verdict parsing/schema | none |
| `contextasm` | Prompt/context assembly and pressure policy: providers, tiers, suppression, eviction, planner, `Prepare` | `jobstate` |
| `repair` | Pure repair policy: budget, candidate choice, instructions, observation parsing | `acceptance` |
| `phases` | The seven §13.1 phase orchestrators and their phase-local operations (verify, pod wiring, research, language, fixtures) | `acceptance`, `repair`, `jobstate` |
| `jobstate` | Typed per-job `system_state` scratch accessors and one canonical `Clear` | none |

`internal/engine` remains the facade: lifecycle, `Run`, recovery, cancellation,
budget admission, strategy capture, process scorecard, the LLM `call` gateway,
model/compute selection, and wiring.

`phases` deliberately does **not** import `contextasm`: the root owns `call`,
asks `contextasm` to prepare a prompt, invokes the model, and performs the
lifecycle transitions a pause requires. This keeps assembly policy out of the
phase package and avoids a package name that collides with the ubiquitous
`ctx context.Context` parameter.

### 2. Dependency rules (enforced, not documented)

`internal/gate/gate_engine_layers_test.go` walks the engine tree and fails the
build when:

1. an engine subpackage imports `internal/engine` (upward call);
2. `phases` is imported by anything other than the root;
3. `contextasm` or `repair` imports `phases`, or `acceptance` imports any
   sibling;
4. `phases`, `contextasm`, `acceptance`, or `repair` imports `internal/store`
   or opens a transaction — `jobstate` is the only database owner inside the
   engine tree.

The existing gates (G1 containment, G2 route/envelope, single-connection
discipline) continue to apply recursively.

### 3. Consumer-defined ports and an unexported adapter

The phase package needs about twenty engine operations (call, plan, audit,
transitions, corrections, git, horizon remaining). Exporting those on `Engine`
would turn implementation details into API. Instead:

- `phases.Runtime` and `contextasm.Runtime` are interfaces defined by the
  consuming package;
- the root implements them on an **unexported** `host` type with one-line
  methods, asserted at compile time;
- public types that moved are re-exported as aliases (`ContextQuery`,
  `ContextProvider`, `Evictor`, `ContextPlanner`, …);
- `ErrPaused` and `ErrRepositorySnapshot` are defined once in `phases` and
  assigned to the root variables, so error identity is preserved.

### 4. Seams are read live

`cmd/athanor/serve.go` wires most seams (corrections, memory, insights,
notebook, clarifier, verifiers, budget ledger, model selector) **after**
`engine.New`. `contextasm.Runtime` and `phases.Runtime` therefore expose
getters (`Chunks()`, `Corrections()`, `Verifiers()`, …) that read the current
fields; they must not capture snapshots at construction. Only
construction-time services (config, store, repositories, artifact/evaluation
stores, tool runner) are passed by value at `New`.

### 5. `engine.Deps`

`New` takes a named `Deps` struct instead of fourteen positional arguments.
The `Set*` setters remain for optional late wiring.

### 6. Tests split by kind, not by file count

All 57 env-driven behavioral tests stay in `internal/engine`; they drive
`Run` and are the behavior-preservation oracle. Only the 13 pure tests whose
subject moves (decision tables, rubric, parser, schema, prompt builders,
diversity, language, fixtures) move with their code. New packages also get
focused unit tests.

## Consequences

- The root package drops to roughly 3.5k production lines; phase, decision,
  assembly, and repair changes become reviewable in isolation.
- A new package cannot bypass acceptance policy or touch the database without
  failing CI.
- The split is behavior-preserving: no schema, config, event-name, or
  acceptance-rule change. The only public signature change is `New(Deps)`.
- Moving `DecideWinner` and the parser to `acceptance` updates living proof
  paths (`docs/claims.md`, README, `docs/cognitive-operations.md`) and the
  `make fuzz` path; historical ADRs and probe documents keep their original
  paths.
- `jobstate.Clear` replaces the hand-maintained `jobScratchKeys` list, closing
  the class of "new scratch key leaks forever" defects.
- Residual root responsibilities (strategy capture, process scorecard, model
  selection, the `call` gateway) stay together because they are the lifecycle
  and judgment spine; a later ADR may split them if they grow.

## Evidence (2026-11-07)

- `make check` (lint, vet, race) green; `make cover-check` green with floors for
  `acceptance`, `jobstate`, and `repair`. `phases` and `contextasm` are
  intentionally unfloored locally: their orchestration is exercised by the
  engine integration suite, so the package-local number understates coverage.
- `make fuzz` green for both targets, including `FuzzParseVerdictJSON` at
  `internal/engine/acceptance/`.
- `make core-smoke` green: the Core Pod boots and serves `/healthz`.
- Diagnostic host smoke (one non-code task): `text-constrained-email` completed
  and passed. A code task (`code-bank-atomic`) failed on the unsupported host
  path; this is a host diagnostic, not a headline, and the full Core Pod slice
  remains the real benchmark gate.


## Rejected alternatives

- **Keep one package, split files.** No boundary enforcement; the 34-file
  package is already the problem.
- **Export the engine methods phases need.** Pollutes the public surface and
  makes internal wiring look like a contract.
- **Move `call` into `phases` or `contextasm`.** Reorders audits and puts the
  LLM gateway and lifecycle transitions behind a second owner.
- **Make `repair` own the loop.** It would need `phases.evaluateCandidate`,
  creating a cycle. `repair` stays pure; the loop stays in `phases`.
- **Name the context package `context` or `ctx`.** Both shadow the ubiquitous
  `ctx context.Context` parameter; `contextasm` states the job and collides
  with nothing.
- **Full hexagonal rewrite.** Too much risk for a behavior-preserving
  structural change.
