# Structured Model Tiers — LAMs, LOMs, and Verifiers

**Status:** design note / hypothesis · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §11–§19, §25; [self-improvement note](self-improvement.md); [F4 plan](f4-plan.md); [security review](security-review.md)

Athanor currently uses general LLMs for everything: generation, planning, and
judgment. This note positions the emerging classes of **purpose-built,
structured-output** models against Athanor's tiers, maps each to the data the
project already produces, and states the safety boundary they must respect.

## 1. The classes

| Class | Predicts | Trained on | Output | Goal | Nature |
|---|---|---|---|---|---|
| **LLM** | next token | web text | prose | generate | declarative-ish |
| **LAM** (Large Action Model) | next action(s) | action **trajectories**, demonstrations | tool calls, API calls, UI actions | **act reliably** | procedural (agency) |
| **LOM** (Large Ontology Model) | ontology ops / structured reasoning | data→ontology + reasoning traces | graph edits, structured answers | **reason deterministically** | declarative (knowledge) |
| **Verifier / reward model** (ORM, PRM) | a score or verdict | preference / outcome labels | scalar or structured verdict | **judge** | evaluation |
| **Encoder-only / small classifier** | a label | labeled examples | structured labels | classify | evaluation |

Notes on provenance and maturity:

- **LAM.** Microsoft Research's "Large Action Models: From Inception to
  Implementation" (2024) and the **UFO** Windows GUI agent; Salesforce's
  **xLAM** family competes on function-calling leaderboards; Anthropic's
  computer-use and similar 2025–26 agents are practical action models. The term
  is also used loosely in marketing (Rabbit). The rigorous version is *a small
  LM fine-tuned to predict the next action*.
- **LOM.** Yonyou AI Lab (2026): a Construct–Align–Reason pipeline that builds a
  structured ontology from raw data and reasons over it deterministically
  (LOM-4B claims ~88.8% ontology completion / ~94% graph reasoning). A single
  lab's claim; treat as a *pattern*, not an off-the-shelf product.
- **Verifiers.** Outcome/Process Reward Models and LLM-as-classifier are
  established; the literature's lesson is that **verification is easier than
  generation**, and a narrow learned verifier beats a general LLM on
  cost/latency/consistency for repeated decisions.

## 2. The tiered stack Athanor wants

```
LLM (generate)  →  LOM-style reasoner (govern knowledge, plan, verify)
                →  LAM-style action model (choose and emit tool calls)
                →  Deterministic verifiers + frozen judge (gate accept/reject)
                                          ▲
                          the immutable safety boundary (§4)
```

Each tier does what it is best at; none replaces the others. The LLM explores
and writes; the LOM governs structured reasoning and consistency; the LAM acts
inside the closed tool space; the verifier/judge decides.

## 3. Where each tier lands in Athanor

### 3.1 LOM — planner / verifier / knowledge tier

- **Fit.** Athanor's object model (§5) *is* a typed graph: `project → goal →
  task → job → artifact → evaluation → correction`, plus strategy
  profiles/outcomes/insights and the append-only event log. The DAG is a graph;
  the scheduler is already a pure graph function.
- **Uses.** DAG planning and validation; dependency/impact ("what blocks if
  this fails?"); consistency/audit ("does this artifact satisfy the project's
  archetype constraints?"); rubric verdicts expressed as structured graph
  queries.
- **Data.** The schema itself; `decompose` prompts; `EvaluationRecord`s; the
  event log.

### 3.2 LAM — the action / tool tier

- **Fit.** Athanor has a **finite, closed action space**: the §25 tool set
  (`execute_code`, `run_tests`, `lint`, `fetch_url`, `search_web`,
  `context_swap`, `query_memory`). That is an ideal target for a compact action
  model that emits a tool call directly.
- **Uses.** Tool selection and arguments once the agent (rather than the fixed
  engine sub-steps) chooses what to do; a small local function-calling model
  instead of a large general LLM routing tools.
- **Data.** Accepted jobs' tool-call logs and `Action` rows (positive
  trajectories); rejected candidates and `CorrectionRecord`s (negative signal);
  the envelope is the action grammar.
- **Maturity.** Highest-risk tier (see §4).

### 3.3 Verifier / reward models — the judgment tier

- **Fit.** The `security` persona is slow and drift-prone (the probe hit
  runaway JSON and type drift). A distilled verifier that emits a structured
  verdict (`{passed, score, missing_criteria[]}`) is faster and more
  reproducible, and grammar-constrained decoding guarantees the structure.
- **Uses.** Rubric scoring; prompt-injection classification; hallucinated-path
  detection; (later) a Process Reward Model over phase traces.
- **Data.** The `security` persona's own verdict history as the **teacher**
  (distillation); `CorrectionRecord`s as negative labels.

### 3.4 Encoder-only classifiers — cheap, deterministic side-tools

- Inject/benign classification, "does the artifact reference a nonexistent
  file", language/task routing. Code, not generation; the first thing to build.

## 4. The safety boundary (mountains vs rivers)

These are "rivers" — capabilities that may evolve — bolted to the "mountains"
that never move:

1. **The frozen judge gates accept/reject.** A learned verifier *advises*; the
   frozen `security` judge (or a quorum of frozen judges) decides. Weight-level
   or learned artifacts never replace the mountains.
2. **A LAM only proposes; the gates adjudicate.** The action model cannot bypass
   the per-job **envelope**, the containment gates (**G1/G2**), or **HITL** for
   external/irreversible actions (§20).
3. **Grammar-constrained output.** Structured models emit against a schema
   (`format: <schema>`), so malformed output is structurally impossible at the
   wire level — directly addressing the runaway/unterminated-JSON failures.
4. **Every learned artifact is versioned, reversible, and HITL-promoted.** Same
   rule as the [self-improvement note](self-improvement.md): draft artifact →
   eval → approval → one-command rollback; a `drift` alarm on behavior change.
5. **Reward-hacking audit.** A learned verifier that contradicts a deterministic
   validator is distrusted; the contradiction is audited and resolved in the
   validator's favor.
6. **Scope isolation.** Learned tooling inherits the same rule found in the
   [security review](security-review.md): no pod-supplied identifier selects a
   scope; scopes are derived server-side.

## 5. Relationship to the roadmap

- **F4** builds the seams that make a learned verifier *substitutable*: the
  verifier interface (F4-T3), judge calibration/quorum (F4-T4), and the compute
  `Policy` (F4-T1). F4 does not require ML; it makes ML pluggable.
- **M7 Daydreaming** is the host for idle-time training/distillation.
- A future milestone (informally `F5`/`M8`) could own the training pipelines.

## 6. Recommended phasing

1. **Deterministic validators first** (tests, schema, path checks) — no ML.
   (This is F4-T3's baseline.)
2. **Encoder-only classifiers** for cheap side-decisions.
3. **Distill the frozen judge into a small structured verifier**, trained from
   its own verdict history; keep the frozen judge as the teacher and backstop.
4. **LOM-style structured reasoner** for planning/consistency over the object
   model (formalize the ontology explicitly).
5. **LAM-style action model** last — highest risk, needs a mature trajectory
   corpus and the gates already proven.

## 7. Open questions

- How large must each corpus be before a distilled model beats a well-tuned
  prompt? (The field's repeated lesson: **prompting first**.)
- Can a LOM-style model be expressed as a pure deterministic query layer over
  the object model (no learned weights), capturing most of the benefit with
  none of the training risk?
- Does a LAM-style action model add value before the engine supports open tool
  selection (today the Core drives fixed sub-steps)?
- How to keep candidate **diversity** from collapsing under distillation
  (F4-T5's Jaccard floor is the natural guard).

## 8. Sources

- Microsoft Research, *Large Action Models: From Inception to Implementation*
  (arXiv 2412.10047); the UFO GUI agent.
- Salesforce **xLAM** action-model family.
- Yonyou AI Lab, *LOM: Unifying Ontology Construction and Semantic Alignment
  for Deterministic Enterprise Reasoning at Scale* (2026).
- Outcome/Process Reward Models; DPO/KTO/SimPO preference optimization;
  knowledge distillation / self-distillation; catastrophic forgetting and
  safety-alignment erosion under fine-tuning.
