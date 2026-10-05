# Integrating LOMs and LAMs into Athanor

**Status:** design note / deep dive · **Date:** 2026-10-05 · **Refs:** [model tiers](model-tiers.md), [self-improvement](self-improvement.md), [F4 plan](f4-plan.md), [security review](security-review.md); ARCHITECTURE §5, §10, §13, §19, §25

A concrete, opinionated proposal for integrating **Large Ontology Models
(knowledge/reasoning)** and **Large Action Models (action/tool use)** into
Athanor, grounded in the current research, with out-of-the-box directions and
open questions to argue about.

## 1. Executive summary

The research converges on one architecture: **internalize a schema-constrained
structure, then reason/act over it with small specialists.** Athanor already has
most of the raw material — a typed object model, an event log, a DAG, a chunk
store, and an accumulating corpus of judgments. The proposal is not to adopt
Yonyou's or Microsoft's models, but to adopt their *pipelines*:

- **LOM → an ontology layer + hybrid reasoner** over Athanor's existing object
  model, used for planning, verification, and audit. Much of it can be **pure
  deterministic queries (no ML)**.
- **LAM → a small execution-verified function-calling model** over Athanor's
  *closed* tool set, trained from accepted-job trajectories, gated by the
  envelope and HITL.
- Both sit behind the **frozen judge** and the F4 `Policy` seam.

## 2. What the research actually says

### 2.1 LOM (Yonyou AI Lab, arXiv 2602.00029 / 2604.09608)

- **Construct–Align–Reason (CAR)**, end-to-end. *Construct* = ontology
  construction from structured + unstructured data: document parsing → **triple
  extraction** → entity disambiguation → graph construction. (Concretely: a
  local Qwen3 at **temp 0.1**, 2,000-token chunks / 150 overlap, extracting
  entities and relations.)
- *Align* = graph-aware encoder + RL to **match generated text to ontology
  structure**; real-time mapping between graph nodes and real entities.
- *Reason* = generative reasoning over a **heterogeneous graph**; the key claim
  is **internalization** — it "thinks within a self-contained, consistent
  universe" rather than fragmenting into external lookups, enabling deep
  multi-hop reasoning. LOM-4B: ~88.8% ontology completion, ~94% graph reasoning.
- **Takeaway for us:** a small model + a strong schema beats a big model +
  no structure, *for structured reasoning*.

### 2.2 LAM (Microsoft Research, arXiv 2412.10047; UFO agent)

- A **full pipeline**: data collection → training → grounding → evaluation.
  *Dataflow* instantiates task-plan data into task-action data (template →
  prefill → filter), then executes it. Training is multi-phase (general task
  plans first, then fine-tuning on action execution with RL signals).
- The LAM is the **inference engine** inside an agent loop: environment state →
  LAM → action → ground/execute → repeat. **Evaluation is treated as critical
  because LAMs change their environment.**
- **Agent Lightning** (Microsoft): a thin layer to **RL-train any agent from its
  own interaction data, without modifying the agent code** (OpenAI-compatible
  API in front of a training framework). This is directly reusable as a
  pattern.

### 2.3 KAG / GraphRAG (Ant Group, arXiv 2409.13731; Microsoft GraphRAG)

- **KAG** = logical-form-guided reasoning over a **schema-constrained** KG, with
  **mutual indexing between graph nodes and source chunks**, and a **hybrid
  reasoning engine** orchestrating *retrieval + KG reasoning + language
  reasoning + numerical calculation*. It explicitly targets the noise of
  open-extraction GraphRAG via semantic alignment.
- **Takeaway:** this is the missing "how do the MCE chunks and the object model
  connect" layer — a schema-constrained graph with chunk↔node mutual indexing.

### 2.4 Small action models (Salesforce xLAM / APIGen)

- **APIGen**: an automated pipeline producing **execution-verified**, diverse
  function-calling data (syntactic + execution + semantic checks). 60k examples,
  3,673 APIs, 21 domains.
- **xLAM-1B/7B** beat much larger models on function calling (7B ≈ GPT-4 on
  BFCL); training mixes **~50% execution-verified function-calling data + 50%
  general instructions**, with interleaved thought + `tool_calls` JSON.
- **Takeaway:** tool calling is a *small-model* problem when the data is
  execution-verified. QLoRA on a 1–8B model is enough.

## 3. Mapping to Athanor (concrete)

### 3.1 The ontology layer (LOM)

| LOM stage | Athanor today | Proposed |
|---|---|---|
| Construct | schema (§5), MCE indexing, project docs | `wide` persona extracts **triples** from goals/docs/code (temp 0.1, 2k/150) → deterministic validation → ontology |
| Align | none | map extracted entities to project/task/artifact/job records; disambiguation; schema constraints |
| Reason | DAG validator + scheduler (pure graph funcs) | a **hybrid reasoner**: retrieval (`query_memory`) + graph ops (reachability, topological, centrality, constraint checks) + language + numeric (budgets) |

Concretely: a new `internal/ontology` package holding
- **typed entities/relations** (reusing the object model: project, goal, task,
  job, artifact, evaluation, correction, plus strategy entities),
- **constraints** (archetype rules, budget bounds, dependency acyclicity),
- a **query/reason API** (pure, unit-testable), exposed to the engine as a tool
  (`query_ontology` / `reason_ontology`) and to the planner.

Most of the *reason* stage needs **no ML** — it's graph algorithms over data
Athanor already has. That is the lowest-risk, highest-value half.

### 3.2 The action layer (LAM)

- **Action schema.** Define the closed action space — the §25 tools plus Core
  actions (git commit, artifact create) — as a **JSON schema**. The per-job
  **envelope is the action grammar**.
- **Trajectory corpus.** Export accepted jobs' tool-call logs and `Action` rows
  as execution-verified trajectories (APIGen-style: each action actually ran).
  Rejected candidates give negative signal.
- **Training.** QLoRA a 1–7B model on interleaved thought + `tool_calls`, then
  constrain decoding with the action schema.
- **Integration.** A `ToolPolicy` seam the engine consults when it needs to
  choose an action; `nil` preserves today's fixed sub-steps. The LAM **proposes**
  — the envelope, containment gates, and HITL adjudicate.

### 3.3 The verifier tier

Already in [self-improvement](self-improvement.md) and [model-tiers](model-tiers.md):
distill the frozen `security` judge into a small structured verifier
(`{passed, score, missing_criteria[]}`), grammar-constrained, audited against
deterministic validators. This is the first and safest ML step.

## 4. The neuro-symbolic loop (the whole picture)

```
        goal ──► [LOM reasoner] ──plan/constraints──► [LLM] candidates
                   ▲     │                                  │
             ontology    │                                  ▼
             (graph)     └──────────► [Verifier / frozen judge] ──accept/reject
                   ▲                        │
                   │                        ▼
              [Corrections → ontology rules]  [LAM action model] ──► tools
                   ▲                                              │
                   └──────────── outcomes / trajectories ─────────┘
```

- **LOM** internalizes the plan and constraints (symbolic).
- **LLM** explores candidates (neural).
- **LAM** acts inside the closed tool space (neural, bounded).
- **Verifier/frozen judge** gates accept/reject (deterministic).
- **Corrections** become ontology rules and training signal (closed loop).

## 5. Outside-the-box directions

1. **Ontology-native memory.** Stop treating the object model, the MCE chunks,
   and strategy data as three stores: one graph, with **chunk↔node mutual
   indexing** (KAG). The Dormant Index becomes *graph neighborhoods*; a
   `context_swap` becomes a graph walk.
2. **Corrections as executable ontology constraints.** Each `CorrectionRecord`
   compiles to a rule the reasoner checks deterministically — prescriptive
   feedback becomes a *formal* constraint, not just a prompt line.
3. **Strategy insights as mined subgraph patterns.** "Divergence led by
   `alternative` wins on code" is a pattern over the strategy subgraph; the
   reasoner evaluates it, the LLM only phrases it.
4. **Bitemporal event log.** The append-only log already gives time travel —
   expose "what did the agent believe/accept at time T?" as an ontology query.
   This is a strong audit/trust feature.
5. **Counterfactual replay for LAM training.** Replay past jobs with alternate
   actions against the recorded state, collect outcomes — synthetic
   execution-verified trajectories *without* new live runs (the APIGen idea,
   using Athanor's own history as the environment).
6. **A world model of the pod/workspace.** Predict the effect of an action
   (test pass/fail, lint) before running it — a model-based agent that plans
   against a learned simulator, then verifies for real.
7. **Small-model factory (idle-time).** During Daydreaming, continuously distill
   the frozen judge + `tall` LLM into tiered specialists (verifier → planner →
   actor), each promoted via HITL only when the eval beats the incumbent.
8. **Self-play adversarial loop.** The LOM planner proposes plans; the LAM actor
   tries to execute them; the verifier is referee. Failures become hard cases
   for the next training round.
9. **Shadow LAM.** Run the action model in *propose-only* mode (it emits the
   action; the current fixed path executes) until its proposal accuracy is
   proven — then flip it on per task class.
10. **Deterministic-by-construction.** Express as much of the "reasoning" as pure
    ontology queries as possible; keep the learned part to *proposing* structure.
    The more the symbolic half carries, the less the safety boundary has to
    police.

## 6. Risks and how Athanor's design absorbs them

| Risk | Mitigation already in Athanor |
|---|---|
| LOM error propagation (the paper's own warning) | deterministic validation of every proposed triple/edge; HITL for ontology changes |
| LAM acting wrongly | closed tool set + envelope + containment gates + HITL for external/irreversible |
| Reward hacking (learned judge) | frozen judge arbitrates; contradiction with a deterministic validator is audited and resolved to the validator |
| Poisoning (malicious docs/trajectories) | airlock + prompt-injection scan on all ingested content; only accepted episodes train |
| Diversity collapse under distillation | F4-T5 Jaccard floor; keep the exploration persona |
| Ontology drift vs schema migrations | the ontology is generated from the schema; a link-checker-style test pins the mapping |
| Complexity | phased: queries first, ML last; each tier nil-safe and reversible |

## 7. Minimal first steps (low-risk, in order)

1. **Define the ontology** as data (typed entities/relations/constraints); no ML.
2. **Build the deterministic reasoner** (graph queries) + a `query_ontology`
   tool; reuse the DAG validator and scheduler.
3. **Export the training corpora** (SQL → JSONL): corrections (KTO), accepted
   vs rejected (DPO), tool logs (trajectories), verdicts (verifier SFT).
4. **Distill the verifier** (smallest ML win) and wire it behind the F4 verifier
   seam, with the frozen judge as backstop.
5. **LOM-style extraction** (`wide`, temp 0.1, chunked triples) into the
   ontology, validated deterministically.
6. **LAM last**, in shadow mode, once the engine supports open tool selection
   and the trajectory corpus is large enough.

## 8. Decisions (from review)

1. **The ontology is explicit** — a typed schema plus a reasoner, not just ad-hoc
   graph queries over the SQLite tables.
2. **A learned LOM is not necessary.** The reasoning tier stays deterministic;
   the learned tiers are the verifier and (later) the actor.
3. **Verifier first.** The first ML investment is a distilled structured verifier
   behind the F4 seam, with the frozen judge as teacher and backstop.
4. **Open tool selection: yes** — so a LAM is justified. The catch: there is no
   usable open-weight LAM to adopt today, so this is a *train-it-ourselves* goal,
   not an integration of someone else's model.
5. **A training pipeline is in scope and first-class.** It must improve over time
   and run on the hardware we actually own: an M1 Pro (16 GB) and an M2 Max
   (32 GB) — the Apple-Silicon MLX/Metal path — and a Linux box (Ryzen 7 3700X,
   32 GB, GTX 1080 Ti, 11 GB VRAM) — the CUDA path. That means small
   QLoRA-class base models (≤3B on the 1080 Ti), shared corpus exporters, and a
   portable training entry point rather than a single-vendor notebook.
6. **Ontology governance: still open.** Migrations-as-source-of-truth vs a
   user-editable ontology file. Proposed default: the schema stays code-owned;
   the ontology is a *generated, versioned view* of it, pinned by a link-checker
   test (mountains = schema, rivers = the ontology view).

## 9. Sources

- Yonyou AI Lab, *Construct, Align, and Reason: Large Ontology Models for
  Enterprise Knowledge Management* (arXiv 2602.00029; 2604.09608).
- Microsoft Research, *Large Action Models: From Inception to Implementation*
  (arXiv 2412.10047); the **UFO** agent; **Agent Lightning**.
- Ant Group, *KAG: Knowledge Augmented Generation* (arXiv 2409.13731); Microsoft
  **GraphRAG**.
- Salesforce, **APIGen** (arXiv 2406.18518) and the **xLAM** function-calling
  family.
