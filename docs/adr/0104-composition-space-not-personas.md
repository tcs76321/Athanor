# ADR 0104 — The model composition space supersedes fixed personas

**Status:** Accepted — design; implementation begins with the qualification and
ranking slice · **Date:** 2026-11-05 · **Refs:** ARCHITECTURE §12, §13, §24;
[ADR-0044](0044-compute-policy-seam.md) (selection seam),
[ADR-0049](0049-model-onboarding-qualification.md) (qualification),
[ADR-0072](0072-single-slot-execution.md) (single-slot execution),
[ADR-0096](0096-capacity-aware-concurrency.md) (capacity),
[ADR-0099](0099-measured-model-fit.md) (measured fit),
[ADR-0100](0100-acceptance-and-feedback-evidence.md) (grounded outcomes),
[ADR-0102](0102-separate-host-and-runtime-memory-budgets.md) (memory budgets),
[ADR-0103](0103-household-operating-modes.md) (household modes).

## Context

Athanor's runtime vocabulary is five fixed roles — `wide`, `tall`, `main`,
`security`, `alternative` — selected by phase in `internal/engine/strategy.go`
and resolved through `llm.Registry`. That vocabulary was a useful first
approximation, but it is a lossy projection of what the system is for:

1. **It conflates independent axes.** `wide` and `tall` describe resource and
   quality envelopes. `main` describes a balance. `security` describes a
   *relationship* (usually "a different family") and a *stance* (judgment).
   `alternative` describes a second opinion. These are not the same kind of
   thing, and encoding them as peer slots forces arbitrary choices.
2. **`security` became a grab bag.** It is the default judge persona, the MCE
   compaction persona, and the daydream feedback/strategy reviewer. It also
   collides with the actual `security:` scanning subsystem. Compaction gains
   nothing from cross-family judgment, and evaluation gains nothing from being
   forced into a role whose primary definition is "not the generator."
3. **It cannot express the design space.** Athanor should be able to use
   multiple models, in multiple ways, with different dialectical perspectives,
   different context views, and different topics of focus, in combination. A
   phase→role table cannot represent "three generators with different repo
   slices, one critic with the tests, one independent verifier, and a
   synthesizer," still less vary that per job.
4. **Qualification is not selection.** ADR-0049 already measures installed
   models and emits an advisory persona→model table. The measurement machinery
   is useful; the *table* is the wrong target. Real outcomes are recorded in
   `strategy_outcomes` but do not update future choices, so the system does not
   learn that a model does better on this computer, in this project, for this
   kind of work.
5. **The user's only lever should be the repertoire.** Letting a user rerank
   models per capability or per job injects unmeasured preference into a
   learned policy and makes results unattributable. The honest lever is
   membership: what Athanor may consider.

The goal is per-job, per-project, per-computer, per-user selection, seeded by
an Athanor-specific qualification benchmark and updated by grounded results
over time.

## Decision

### 1. Composition primitives, not a persona roster

The engine-facing concepts are:

- **Model** — any installed model, described by a measured profile (family,
  digest, quant, resident size at context, maximum context, speed, JSON/schema
  reliability, tool-calling, language support, verifier calibration).
- **Stance** — what a call is doing: plan, generate, critique, synthesize,
  verify, compare, red-team, explore, translate, simplify. Dialectical
  perspective lives here, not in a model slot.
- **Context view** — the slice a call sees: goal, constraints, repository
  files, tests, memory, prior attempts, exemplars, user preferences.
- **Focus / aspect** — what the call is asked to care about: correctness,
  security, performance, UX, data integrity, maintainability, accessibility,
  cost. Aspects can be generated per task.
- **Params** — temperature, seed, context budget, output schema, tools.
- **Attempt** — the tuple of the above.
- **Deliberation** — a graph of attempts with dependencies, an aggregation
  policy (select/merge/vote/synthesize), deterministic verifiers, and a
  budget. Templates are the shipped repertoire: solo, best-of-N, heterogeneous
  panel, thesis/antithesis/synthesis, multi-aspect review, cheap-to-expensive
  cascade.

`wide`, `tall`, `main`, `security`, and `alternative` survive only as named
**presets** that expand into requirements over the pool. Presets are UX, not
ontology. A user may edit or omit them. Nothing in the engine requires a role
name.

### 2. Pool and roster are different layers

- The **pool** is user-controlled membership: models pulled into Ollama and
  allowed for Athanor. The user adds and removes models freely.
- The **roster** is Athanor-controlled eligibility and ranking: models that
  passed qualification, ordered by measured performance on this machine. The
  user cannot promote or rerank a model, only remove it from the pool.
- A newly added model is `pending` and cannot be cast until measured. Removing
  a model affects new jobs; an in-flight job finishes with its current cast.
- There is no per-job pinning or manual weighting in the normal UX. A debug-only
  override may exist for development, but it is not a supported control.

### 3. A real minimum, a sanity maximum

- **Minimum four models** for the full experience: enough for a generator, a
  critic, an independent perspective, and a long-context/small alternative.
  Fewer models put the system into an explicit **degraded mode** (2–3:
  heterogeneous panels disabled; 1: "single-model, not the full Athanor
  experience"), surfaced in setup and on the dashboard. It is a strong warning,
  not a silent block.
- **Maximum 100** only as a sanity bound; disk and benchmark time are the real
  limits.
- Models need not differ in size or family. Diversity is **measured**, not
  required; when four models are the same family and size, the policy leans on
  stance and context diversity rather than pretending it has independent
  judges.
- Ties are broken deterministically by model name, never by map iteration or
  install order.

### 4. Selection is scoped

The policy chooses attempts and deliberation templates under a hierarchy:

| Scope | What it conditions |
|---|---|
| user | quality-vs-speed and risk preferences, acceptance history |
| computer | fit, context ceiling, throughput, model-swap cost, machine score |
| project | domain, language, repository shape, conventions, local history |
| job | capability, difficulty, context needs, novelty, risk |
| attempt | model, stance, view, focus, params |

Evidence is combined by **partial pooling**: a new project inherits the user
and computer priors; it earns its own posterior only after enough grounded
jobs. One lucky or unlucky result never overrides the prior. Results do not
transfer blindly between computers; they are keyed by hardware and model
digest.

### 5. Two evidence sources, one authority

- **Qualification is a prior.** An Athanor-shaped suite measures per capability
  and stance, with uncertainty, not a single leaderboard: repo editing with
  real tests, schema-conformant data, constrained document synthesis,
  long-context retrieval, tool calling, instruction adherence, multi-language,
  injection resistance, and verifier calibration. It is tiered:
  1. **Screen** (seconds/model): fit at required context, metadata, digest.
  2. **Smoke** (minutes/model): one small grounded case per capability.
  3. **Deep** (hours): top and maximally diverse subset, run in Deep/idle.
  Results are keyed by `(model digest, quant, context, runtime, machine)` and
  invalidated when any of those change. The suite is separate from the frozen
  capability benchmark (ADR-0075) and may evolve with the models it measures.
- **Real jobs are the update.** Only grounded signals count: tests, schemas,
  deterministic verifiers, human accept/reject, cost, wall time. A model's own
  verdict never trains the policy.
- **Deterministic verification remains authoritative.** LLM perspectives argue
  and advise; frozen gates and hard verifiers decide. The kill switch, HITL
  boundary, and containment are unchanged.

### 6. The machine is a first-class condition, not a weight

Each computer gets a measured **machine profile** and a 0–100 capability score
(memory, GPU, context ceiling, throughput, benchmark timings). Model ranking is
**predicted verified performance on this machine**; a model that scores 95
elsewhere and 40 here is ranked as a 40 here.

There is no global "70% machine / 30% model" split. The blend is per task: a
long-context job weights memory and context, a quick code job weights tool
reliability and tokens/sec. The machine profile conditions the prediction and
is shown in the UI; it does not freeze a ratio.

### 7. Budgeted, consent-aware exploration

Learning requires occasionally trying a model the policy does not currently
prefer. Exploration is:

- bounded by tokens, wall time, model swaps, and memory;
- allowed on low-risk, reversible, unattended work (principally Deep);
- never used on irreversible or high-risk actions;
- visible in the UI and recorded in the decision log;
- protected by a diversity floor so the policy cannot collapse to one model.

Heavy qualification runs in Deep; Full may run smoke probes; Lite runs nothing.

### 8. Explainable by construction

Every cast records why it was chosen: the scopes consulted, the measured
evidence, the expected cost, and any exploration. The UI shows the ranking,
relative performance, confidence, and the reason for a job's cast. The user's
recourse is to change the pool — not to overrule a measured ranking.

### 9. Learning over templates first

The first policy is a contextual bandit over deliberation templates and
participant choices, with hierarchical priors from qualification and partial
pooling across scopes. Template-level rewards come first; per-participant
credit assignment (leave-one-out replay) comes later. Promotion is gated by the
same ratchet as other policy changes: beat the incumbent, stay reversible,
alarm on drift. The repertoire is never pruned; selection is learned.

## Migration

1. **Model profiles and ranking.** Extend `internal/onboard` beyond the
   role-oriented ladder to per-capability and per-stance measurements, a
   machine profile, and a ranked roster with confidence. Keep
   `qualification.json` as the durable record; add a schema version.
2. **Selection package.** Add a `selection` package with scope keys, features,
   arms (template first), priors, and an update rule. Run it in **shadow mode**
   beside the current phase→role defaults, logging decisions and outcomes
   without changing behavior.
3. **Cast resolution.** Replace `strategy.go`'s phase→role table with a
   requirement→cast resolver. Legacy `personas:` entries become presets that
   resolve into the pool; `security` maps to the `verify`/`memory` presets and
   `alternative` to an opinion slot, preserving current behavior until the
   policy is enabled.
4. **Enable and measure.** Compare shadow decisions with the incumbent on real
   jobs; enable per capability once the evidence is sufficient. Record every
   switch.
5. **Remove role assumptions.** Update prompts, doctor, audit, compaction,
   daydream, and UI to speak of models, stances, views, and casts rather than
   five roles.

## Consequences

- The system can represent and choose across multiple models, stances,
  contexts, and focuses without naming every combination.
- The user's control surface is small and honest: the pool.
- The memory budget becomes per-cast: which models must be resident together,
  and what a swap costs. On a single GPU, "same model, different stance" is
  often correct; cross-family independence is a deliberate expense.
- Sparse data is expected at the project and user scopes; partial pooling keeps
  the policy from overfitting small samples.
- Qualification cost is bounded by tiering, not by the pool size.
- Model upgrades invalidate priors; re-measurement is required, and the policy
  says so rather than silently degrading.
- The five role names remain as presets for compatibility, but no new code
  should depend on them.

## Alternatives considered

- **Keep the five roles and add more.** Rejected: more names do not create a
  composition space and still conflate model, stance, and relationship.
- **One best model everywhere.** Rejected: it is the single-shot behavior
  Athanor exists to avoid, and it correlates verifier errors.
- **Let the user rerank or weight models.** Rejected: unmeasured preference
  warps the policy, makes outcomes unattributable, and asks the user to do the
  system's job.
- **Full benchmark for every installed model.** Rejected: cost grows with the
  pool; screen → smoke → deep gives a usable ranking in minutes.
- **A fixed machine/quality weighting.** Rejected: task-dependent trade-offs
  make any global ratio wrong; condition on the machine instead.
- **Learn from the model's own verdict.** Rejected: self-judgment is exactly
  the correlated signal the design is trying to escape.

## Open questions

- Is the four-model floor a hard setup gate or a warning with degraded mode?
  The current design says warning plus degradation.
- The exact machine score formula and its calibration against real job
  performance.
- Versioning and refresh cadence for the qualification corpus.
- How to present 100 installed models without turning setup into a catalog.
- Whether preset names survive the migration or are replaced by generated
  labels.
