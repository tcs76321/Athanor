# Athanor — System Architecture

An *athanor* is an alchemical furnace designed to burn continuously without interruption. Athanor is a local-first, container-native, semi-autonomous agent system designed for continuous 24/7 operation. This document is the canonical description of how it works.

---

## 1. Design Principles

- **Local-first.** Storage, inference, and execution stay on your hardware by default. Cloud inference is optional, budget-gated, and requires explicit approval.
- **Containment-based trust.** Contained + reversible = no approval needed. External OR irreversible = approval required.
- **Ephemeral execution.** Every task runs in a fresh, hardened, rootless Job Pod, destroyed on completion. Post-mortem evidence is captured in logs and artifact records, not live containers.
- **Generalized but coding focused.** Coding is central to the architecture and function but not a singular and limiting consideration.
- **Git as undo.** All code modifications are atomic commits on agent-created branches.
- **Lossless over lossy.** Critical context is never summarized away — it is structurally divided and swapped. Compaction, when used, is deterministic (Temperature 0.0).
- **Deterministic judgment.** Evaluation and comparison always run on the `security` persona at Temperature 0.0. Generation explores; judgment does not.
- **Hardware-adaptive.** Ollama handles physical device routing (GPU/CPU). The Core handles capacity planning (context feasibility, OOM prevention) and performance guarding.
- **Agentic context floors.** Agentic work fails at low context. We enforce strict minimum context windows and recommend model changes rather than cripple the context.
- **Feedback compounds.** Rejections with structured reasons are stored as `CorrectionRecord`s and injected into future prompts.
- **Mountains and rivers.** Security constraints never change. Prompts and strategies evolve.
- **Project-first, not chat-first.** The primary unit of interaction is a bounded project with an archetype and acceptance criteria — not a generic chat window.
- **Test and lint everything.** TDD at integration scale. Security tests for adversarial vectors. Scanning at multiple checkpoints.
- **Append-only audit logs.** Everything leaves a footprint. Prompt regressions tracked. Winning strategies analyzed and reused. Losing strategies analyzed and remembered.

---

## 2. Stack

| Layer | Technology |
|---|---|
| Core Pod services (state, MCE, DAG, dialectical engine, gateway, scanner, UI) | **Go** |
| Sandbox / execution isolation | **Rootless Podman** (Core Pod + ephemeral Job Pods) |
| Persistent state | **SQLite** (WAL, FTS5, sqlite-vec) |
| LLM inference | **Ollama** (default backend, REST API) |
| Web extraction | **Go `net/http` + `codeberg.org/readeck/go-readability/v2` + `bluemonday`** (Reader Mode) |

> **Note (Reader Mode dep):** ARCHITECTURE originally named
> `github.com/go-shiori/go-readability`; that module is deprecated upstream
> ("use codeberg.org/readeck/go-readability/v2 instead"). We depend on the
> maintained continuation at v2.1.2 — see [ADR-0018](docs/adr/0018-reader-mode.md) §1.
| Malware / threat scanning | **ClamAV**, **YARA** (optional but recommended) |
| Filesystem events | **`fsnotify`** (Host Adapter) |
| System metrics | **`gopsutil`** |
| Web UI | **HTML/CSS/JS** (vanilla, no framework) |

> **Note (macOS):** Rootless Podman runs inside a VM (`podman machine`) on macOS. The Host Adapter accounts for this VM's overhead in memory budgeting, and sleep/wake coordination includes the podman machine lifecycle. See §12.3 and §30.

---

## 3. System Topology

```text
┌──────────────────────────────────────────────────────────────┐
│ Host Operating System                                        │
│                                                              │
│  ┌────────────────────┐                                      │
│  │   Host Adapter     │  Minimal OS bridge                   │
│  │  (power, sleep,    │  (tray, fsnotify, Podman lifecycle)  │
│  │   idle, fsnotify)  │                                      │
│  └─────────┬──────────┘                                      │
│            │ starts/manages                                  │
│            ▼                                                 │
│  ┌───────────────────────────────────────────────────────┐   │
│  │                    Core Pod (Go)                      │   │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐      │   │
│  │  │  core   │ │ gateway │ │ scanner │ │   ui    │      │   │
│  │  │ (state, │ │ (egress,│ │ (ClamAV,│ │ (watch, │      │   │
│  │  │  MCE,   │ │ reader, │ │  YARA,  │ │  HITL,  │      │   │
│  │  │  DAG,   │ │ cred    │ │ airlock)│ │  dash)  │      │   │
│  │  │ dialect)│ │ broker) │ │         │ │         │      │   │
│  │  └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘      │   │
│  │       └───────────┴───────────┴───────────┘           │   │
│  └───────────────────────────┬───────────────────────────┘   │
│                              │                               │
│              ┌───────────────┼───────────────┐               │
│              ▼               ▼               ▼               │
│        ┌──────────┐    ┌──────────┐    ┌──────────┐          │
│        │  SQLite  │    │  Ollama  │    │  Job Pod │          │
│        │  (WAL)   │    │ Inference│    │ Ephemeral│          │
│        │  State   │    │ Engine   │    │ Execution│          │
│        │  + vec   │    │          │    │          │          │
│        └──────────┘    └──────────┘    └────┬─────┘          │
│                                              │               │
│                                              ▼               │
│                                     Isolated Execution       │
│                                     (read-only rootfs,      │
│                                      no host net,           │
│                                      /workspace + /tmp)     │
└──────────────────────────────────────────────────────────────┘
```

The Core is the **sole router**. No component communicates peer-to-peer.

### 3.1 Isolation Rules

Job Pods may communicate **only** with:

- The Core internal API (via restricted localhost socket).
- Approved workspace volumes (read-only for inputs, read-write for `/workspace/output`).
- Read-only skill volumes.

Job Pods may **not** directly access:

- Ollama or any inference endpoint.
- The public internet.
- The host filesystem outside approved mounts.
- The Podman socket.
- The Core SQLite database.
- Other Job Pods.
- User credentials.

### 3.2 Internal API Authentication

Every Job Pod receives a single-use, short-lived token scoped to its job ID at creation time. The Core internal API validates this token on every request, enforces the tool allowlist for that job, and revokes the token at teardown. Tokens are delivered via a mounted tmpfs file — never via environment variables or command-line arguments.

---

## 4. Directory Layout

```text
~/athanor/
├── config.yaml                  # User configuration
├── state/
│   ├── athanor.db               # SQLite (WAL mode, FTS5, sqlite-vec)
│   ├── logs/                    # Structured JSON logs
│   ├── backups/                 # Auto-backups
│   └── migrations/              # Forward-only schema migrations
├── workspace/
│   ├── inbox/                   # File airlock ingress
│   ├── output/                  # File airlock egress
│   ├── projects/                # Per-project repositories
│   ├── scratch/                 # Temporary job working files
│   └── quarantine/              # Suspicious/rejected files
├── skills/                      # Python skills (OCI or directory)
├── prompts/                     # User-editable prompt templates
└── exports/                     # Artifact exports
```

### Persistence Rules

- `state/` should use a Podman-managed volume if host filesystem locking is unreliable.
- `workspace/` must be host-visible for user inspection.
- `inbox/` and `output/` form the file airlock boundary.
- `quarantine/` stores rejected or suspicious files, isolated from the workspace.

---

## 5. Object Model

| Object | Purpose |
|---|---|
| `Project` | Bounded context for goals, documents, preferences, tasks, artifacts, corrections |
| `Goal` | High-level mission (20–500 chars) |
| `Task` | Unit of work in a DAG; includes `AcceptanceCriteria` and budget |
| `Job` | One execution instance of a task (runs the Dialectical Engine) |
| `Action` | One tool invocation inside a job |
| `Artifact` | Versioned output (code, document, dataset, proposal, media, evaluation) |
| `Skill` | Reusable, versioned Python capability with declared permissions |
| `PromptTemplate` | Versioned prompt definition (static, runtime, user, meta, skill, eval) |
| `ContextBundle` | Selected context items for a specific job |
| `AcceptanceCriteria` | Explicit, testable success conditions for a task |
| `EvaluationRecord` | Deterministic judge/test output comparing artifacts |
| `CorrectionRecord` | Structured negative feedback derived from failures |
| `StrategyProfile` | Record of the personas, temperatures, templates, and candidate counts a job actually used |
| `StrategyOutcome` | Immutable per-job result linked to a profile (acceptance, score, cost, retries) |
| `StrategyInsight` | Derived winning/losing process pattern with evidence, scope, and lifecycle status |
| `ExplorationPath` | Configured multi-step prompt/temperature sequence |
| `HITLRequest` | Approval or user-input request with severity and expiry |
| `EventLog` | Append-only audit trail |
| `NetworkRule` | Egress allowlist entry |
| `ModelPersona` | Abstract model role (wide, tall, main, security, alternative) |
| `DormantIndex` | Table of contents for lossless context chunks available for swapping |
| `DaydreamLog` | Record of idle-time exploration and memory consolidation |

---

## 6. Project Model & Archetypes

### 6.1 Project Fields

```yaml
id: uuid
name: unique-project-name
slug: unique-project-slug
goal: "One sentence describing the objective (20-500 chars)."
archetype: text | code | document | data | media
repository_path: optional
documentation:
  - files
  - notes
  - references
acceptance_criteria:
  - criterion
tasks:
  - task_ids
preferences:
  - project_preferences
corrections:
  - correction_records
synopses:
  - ai_generated_session_summaries
created_at: timestamp
updated_at: timestamp
```

### 6.2 Artifact Archetypes

When creating a project, the user selects an archetype. Each archetype enforces concrete constraints that the Dialectical Engine must respect.

| Archetype | Required Definition |
|---|---|
| `text` | Output format, audience, style |
| `code` | Language, repository path, test framework, build command, run command, linters |
| `document` | Structure, citation style, export format |
| `data` | Schema, validation rules, source format |
| `media` | Toolchain, output format, asset constraints, render command |

Example code archetype:

```yaml
archetype: code
language: python
repo: workspace/projects/example
test_framework: pytest
build_command: "python -m build"
run_command: "python -m example"
linters:
  - ruff
  - mypy
documentation_required: true
```

---

## 7. Autonomous DAG Decomposition

When a `Goal` is submitted, Athanor uses the `tall` persona to autonomously decompose it into a Directed Acyclic Graph (DAG) of `Task` objects.

### 7.1 Decomposition Workflow

1. Read Goal, Project Context, and Archetype constraints.
2. Generate Task tree as a DAG:
   - Identify logical phases (schema, implementation, tests, documentation).
   - Create leaf tasks with explicit `AcceptanceCriteria`.
   - Define dependency edges (e.g., `implement_api` depends on `define_schema`).
3. Validate DAG:
   - Check for cycles.
   - Ensure every leaf task has at least one `AcceptanceCriteria`.
   - Ensure budget allocation is feasible.
4. Submit DAG to user for quick approval (optional; can be auto-approved for trusted goals).
5. Execute tasks respecting dependency order.

### 7.2 DAG Failure & Recovery Policy

| Failure Type | Policy |
|---|---|
| Leaf task fails after max retries | Mark as `blocked`. Attempt `alternative` persona re-decomposition. If still blocked, escalate to HITL. |
| Dependency task fails | Descendant tasks are marked `blocked` until dependency is resolved or user cancels. |
| Budget exhausted for a sub-DAG | Pause remaining tasks. Escalate to HITL for budget increase or scope reduction. |
| DAG decomposition itself fails | Retry with `main` persona (simpler decomposition). If still fails, ask user to refine the Goal. |

### 7.3 Task Object

```yaml
id: uuid
project_id: uuid
parent_task_id: optional
name: string
description: string
task_type: one_time | recurring | triggered
schedule: optional cron
trigger: optional event
archetype: inherited or overridden
required_personas:
  - wide
  - main
  - security
  - tall
  - alternative
allowed_tools:
  - read_file
  - write_file
  - execute_code
  - git_operation
  - query_memory
  - fetch_url
  - context_swap
acceptance_criteria:
  - criterion
budget:
  max_jobs: int
  max_llm_calls: int
  max_tokens: int
  max_wall_time: duration
dependencies:
  - task_ids
status: pending | ready | running | paused | blocked | completed | failed
```

---

## 8. Job Model & State Machine

A `Job` is one execution of a `Task` through the Dialectical Engine.

### 8.1 Job States

```text
queued
context_building
planning            # Dialectical Phase 1; explicit state so planning is observable and resumable
diverging
evaluating          # includes running_tests as a tracked sub-state of evaluation
reflecting
synthesizing
comparing
awaiting_approval
paused
completed
failed
cancelled
```

> **Note on `running_tests`:** test execution is part of the Evaluation phase (Phase 3). It is tracked as a sub-state of `evaluating` rather than a separate top-level state, so a crash during a test run resumes into `evaluating`, not an ambiguous intermediate state.
>
> **Note on `interrupted`:** `interrupted` is a recovery flag recorded on the Job (§8.3) after a crash or power loss — it is **not** a state-machine state. An interrupted job retains its last committed state and resumes from checkpoint per §23.6.

### 8.2 State Machine Rules

- State is serialized to SQLite after **every** transition.
- Transitions are atomic. If the Core crashes mid-transition, recovery resumes from the last committed state.
- `diverging` → `evaluating` is mandatory (no skipping evaluation).
- `evaluating` → `reflecting` only if no candidate passed (otherwise → `synthesizing`).
- `synthesizing` → `comparing` is mandatory.
- `comparing` → `completed` if winner is `new` or `previous`. Otherwise → `failed` or retry.
- Wall-time budgets are enforced **per phase** (see §29 `execution:`), not only per job — a single global timeout cannot fairly cover both a quick reflection and a full test-suite run in a Job Pod.

### 8.3 Job Object

```yaml
id: uuid
task_id: uuid
project_id: uuid
status: state
persona_plan: map
context_bundle_id: uuid
artifact_candidates:
  - artifact_ids
best_artifact_id: optional
evaluation_records:
  - evaluation_ids
correction_records:
  - correction_ids
hitl_requests:
  - request_ids
token_usage:
  prompt_tokens: int
  completion_tokens: int
  estimated_cost: optional
started_at: timestamp
finished_at: timestamp
error: optional
recovery_count: int
interrupted: bool          # recovery flag (not a state): set by crash recovery, cleared on successful resume (§23.6)
```

---

## 9. Artifact Model & Lifecycle

### 9.1 Artifact Types

| Type | Examples |
|---|---|
| `code` | Source files, tests, build scripts |
| `document` | Markdown, PDF, LaTeX, report |
| `proposal` | Plan, design, brainstorm result |
| `dataset` | CSV, JSON, SQLite export |
| `media` | Audio, video, image, rendered output |
| `evaluation` | Test report, comparison report |
| `configuration` | YAML, JSON, environment templates |

### 9.2 Artifact Fields

```yaml
id: uuid
job_id: uuid
project_id: uuid
type: artifact_type
path: workspace_path
version: integer
git_commit: optional
status: draft | candidate | accepted | rejected | quarantined
metadata: map
created_at: timestamp
```

### 9.3 Artifact Status Flow

```text
draft → candidate → accepted
                 → rejected → (feedback generated)
          → quarantined (security scan failure)
```

- `draft`: Generated by the Dialectical Engine during divergence.
- `candidate`: Passed initial evaluation, promoted for comparison.
- `accepted`: Won the comparison phase. Written to workspace. Git committed.
- `rejected`: Lost comparison or failed evaluation. Triggers `CorrectionRecord` if pattern is identifiable.
- `quarantined`: Failed security scan. Isolated. Never written to workspace.

---

## 10. Multidimensional Context Engine (MCE)

Athanor abandons simple 2D progressive summarization in favor of an N-Dimensional Context Gradient that balances **Lossless Division** and **Deterministic Compaction** to prevent information corruption while respecting hardware KV-cache limits.

### 10.1 Full-Fidelity Division (Lossless Swapping)

Critical artifacts (source code, test outputs, exact user constraints) are never summarized. They are structurally divided.

- **Division Engine:** Algorithmic. Splits by AST (Abstract Syntax Tree) boundaries for code, or by structural headers for text/markdown. No LLM inference required. Zero information loss.
- **Active Chunk:** Loaded into the LLM context window.
- **Dormant Chunks:** Held intact in fast local storage (SQLite/RAM).
- **Dormant Index:** A table of contents provided to the LLM in the prompt. Contains chunk IDs, summaries (1-line), and metadata (file path, line range, type).
- **Context Swap Tool:** If the LLM requires dormant information, it issues a `context_swap(target_chunk_id)` tool call. The system flushes the current Active Chunk to Dormant storage and loads the requested chunk perfectly intact.

### 10.2 The N-Dimensional Context Gradient

Memory objects are profiled across three axes to determine their treatment:

| Axis | Dimensions |
|---|---|
| **Epistemic Type** | `code`, `test_output`, `conversation`, `log`, `documentation` |
| **Temporal State** | `active` (current step), `recent` (current job), `episodic` (past jobs), `archival` |
| **Semantic Relevance** | `direct`, `tangential`, `dormant` (calculated dynamically per task via vector similarity) |

### 10.3 Treatment Matrix & Temperature Invariants

To prevent compaction corruption (hallucination, drift, paraphrasing), **all context manipulation operations strictly use Temperature 0.0**.

| Profile Combination | Treatment | Mechanism | Persona |
|---|---|---|---|
| `code` / `test_output` + `active/recent` | **Pure Division** | Algorithmic AST/structural splitting. Zero LLM inference. | N/A |
| `log` / `test_output` + `episodic` | **Deterministic Compaction** | Temp 0.0 LLM pass. Extracts exact error codes, stack traces, return values. Discards verbose noise. | `security` |
| `conversation` / `brainstorming` + `archival` | **Semantic Compaction** | Temp 0.0 LLM pass. Extracts explicit decisions, constraints, derived rules. Discards raw chat. | `security` |
| `documentation` + `episodic/archival` | **Semantic Compaction** | Temp 0.0 LLM pass. Extracts key facts, API signatures, architecture decisions. | `security` |

### 10.4 Real-Time KV Cache Monitoring

The MCE does not only manage context at job start — it monitors KV cache usage in real time during job execution.

**Monitoring Signals:**
- Token count in the active prompt (measured before each LLM call).
- Ollama-reported context usage (if available via API).
- Estimated KV cache pressure: `active_tokens / max_context`.

**Dynamic Triggers:**

| Condition | Action |
|---|---|
| `active_tokens > 85% of max_context` | Issue `context_swap` suggestion to LLM. Move oldest non-pinned, non-critical chunks to Dormant. Provide Dormant Index. |
| `active_tokens > 95% of max_context` | Force-evict lowest-priority tier to Dormant. Inject `Dormant Index` entry. LLM may swap back later. |
| Context floor violation (effective context < task minimum) | Pause job. Recommend smaller model or reduce task scope. Escalate to HITL. |

### 10.5 Context Assembly Priority

This ordering governs KV-cache budgeting and eviction only; prompt construction order is defined separately in §11.2.

When building a prompt for a new job or phase, the MCE fills the available KV cache via a priority queue:

1. **Static System & Security Constraints** (immutable, full fidelity, always loaded)
2. **Current Task & Acceptance Criteria** (full fidelity, always loaded)
3. **Active Code Division Chunk** (full fidelity, the primary working set)
4. **Relevant `CorrectionRecord`s** (Temp 0.0 compacted, injected by vector similarity to task)
5. **Episodic Context** (Temp 0.0 compacted, summaries of past jobs in this project)
6. **Dormant Index** (metadata only: chunk ID, 1-line summary, file path, line range)
7. **Evaluation Instructions & Strategy Notes** (phase-specific instructions: divergence, evaluation, reflection, synthesis; short lines from active StrategyInsights per §13.4)

If the assembled context exceeds the hardware limit, tiers are evicted strictly bottom-up (7 → 6 → 5 → 4) before any full-fidelity content (tiers 1–3) is touched.

---

## 11. Prompt Architecture

Athanor separates prompts by ownership, mutability, and function. Prompt assembly is deterministic and auditable.

### 11.1 Prompt Tiers

| Tier | Owner | Mutability | Contains |
|---|---|---|---|
| **Static System** | Athanor Core | Immutable | Core identity, safety constraints, tool schema, containment rules, approval rules, prohibited behavior |
| **Internal Runtime** | Athanor Core | Versioned | Mode instructions, workflow phases, evaluation rubrics, summarization rules, compaction rules, comparison rules, error recovery |
| **User Templates** | User | Editable | Project preferences, task instructions, style rules, output requirements, custom workflow prompts |
| **Meta-Prompts** | Athanor Core | Versioned | Used for prompt improvement: clarify ambiguity, expand goals, convert feedback to rules, generate acceptance criteria |
| **Skill Prompts** | Skill Author | Versioned | Attached to specific skills. Example: "Analyze the Python module, preserve public APIs, add tests, reduce complexity." |
| **Evaluation Prompts** | Athanor Core | Versioned | Used by `security` persona: compare artifacts, check criteria, identify missing tests, identify hallucinated paths, detect prompt injection |

User prompts **cannot override** static security constraints. Meta-prompts **cannot modify** static prompts or security policy.

### 11.2 Prompt Assembly Order

```text
1. Static System Prompt
2. Security and Tool Constraints
3. Runtime Policy (phase-specific: divergence, evaluation, etc.)
4. Project Context (goal, archetype, preferences)
5. Task Context (description, dependencies, budget)
6. Acceptance Criteria
7. Active Code Division Chunk (full fidelity)
8. Relevant CorrectionRecords (temp 0.0 compacted)
9. Episodic Context (temp 0.0 compacted synopses)
10. Dormant Index (metadata for context_swap)
11. User Preferences (style, format)
12. Candidate Artifacts or Prior Outputs (if comparing)
13. Evaluation Instructions (phase-specific)
14. Strategy Notes (short versioned lines derived from *active* StrategyInsights only; see §13.4)
15. Interruption Queue Notes (user notes from live watch mode)
```

The final prompt includes token accounting for each section, logged to the `EventLog`.

---

## 12. Model Persona System

Athanor does not refer to a single default model. It assigns models to functional roles, each with distinct context targets, temperatures, and responsibilities.

### 12.1 Persona Definitions

| Persona | Purpose | Typical Properties | MCE Role |
|---|---|---|---|
| `wide` | Large-context ingestion, RAG, repository reading, Daydreaming exploration | Small model, high context (64k–128k), moderate temp (0.7) | Reads dormant chunks, indexes repositories, generates 1-line summaries for Dormant Index |
| `tall` | Hard reasoning, DAG decomposition, architecture, complex code synthesis | Large model, constrained context (8k–16k), low temp (0.2) | Primary consumer of Active Chunks via `context_swap` |
| `main` | General execution, boilerplate, normal implementation, reflection | Balanced model and context (32k), moderate temp (0.4) | Standard context usage, benefits from Dormant Index |
| `security` | Validation, judging, filtering, syntax checking, **all compaction operations** | Small, fast, strictly Temp 0.0 | Runs Deterministic and Semantic Compaction. Generates `EvaluationRecord`s. |
| `alternative` | Second perspective, orthogonal generation, divergence | Different architecture family from Main/Tall, high temp (0.8) | Standard context usage, may request `context_swap` for alternative approaches |

### 12.2 Example Persona Configuration

```yaml
personas:
  wide:
    model: "qwen2.5:7b"
    context_target: 65536
    temperature: 0.7
  tall:
    model: "qwen2.5-coder:32b"
    context_target: 16384
    temperature: 0.2
  main:
    model: "mistral-nemo:12b"
    context_target: 32768
    temperature: 0.4
  security:
    model: "phi3:3.8b"
    context_target: 8192
    temperature: 0.0
  alternative:
    model: "llama3.1:8b"
    context_target: 32768
    temperature: 0.8
```

### 12.3 Hardware-Adaptive Context Calculation

Ollama sets context at model load time. Core calculates available context before requesting a load:

```text
max_context = (available_memory - model_size - overhead - buffer) / kv_cost_per_token
effective_context = min(max_context, model_architecture_maximum, persona_context_target)
```

> **macOS:** `available_memory` is budgeted against the podman machine VM limits, not raw host RAM.

If `effective_context < task_context_minimum`:

1. Try another persona.
2. Recommend a smaller model with larger context.
3. Reduce task scope explicitly.
4. Escalate to HITL if quality may be compromised.

**Never silently reduce context below the required floor.**

### 12.4 Performance Guarding

| Signal | Action |
|---|---|
| Inference latency significantly above average | Notify user, suggest smaller model |
| RAM pressure above 85% | Pause background/Daydreaming jobs |
| RAM pressure above 95% | Save state, pause all jobs |
| Battery below threshold | Pause jobs, flush state |
| Thermal throttling | Reduce concurrency |
| System sleep | Flush state and pause gracefully |
| System wake | Resume from checkpoint |

### 12.5 Model Lifecycle

- Load models when needed.
- Reload when context target changes.
- Avoid rapid reload loops (debounce).
- Allow Ollama to unload idle models.
- Prefer reusing already-loaded models for repeated jobs.

### 12.6 Context Floors vs. Persona Targets

Archetype context floors (`context_engine.*_floor`, §29) and persona context targets can disagree — e.g., `coding_floor` is 32768 while `tall` targets 16384. Resolution rules:

- Floors are **role-scoped working-set requirements**, not per-task constants. They bind to the personas whose phases consume full-fidelity content chunks: `main` and `alternative` (generation), `security` (evaluation/compaction over large outputs), and `wide` (ingestion).
- `tall` is deliberately exempt during **Planning and DAG Decomposition phases only**. Those phases operate on task descriptions, acceptance criteria, and Dormant Index metadata — not raw code chunks — so `tall`'s constrained target (8k–16k) does not violate the floor. If planning requires source insight beyond `tall`'s target, ingestion is delegated to `wide` (division + indexing) and `tall` pulls distilled chunks via `context_swap`.
- Feasibility is therefore checked per **(persona, phase)** pair: `required_context = max(persona_context_target, role_applicable_floor)`. Context is never silently reduced below either bound; violations follow the §12.3 escalation path.

> Rationale and rejected alternatives: `docs/adr/0002-context-floor-semantics.md`.

---

## 13. The Dialectical Execution Engine

Complex tasks are solved via a multi-phase, multi-temperature state machine.

**Temperature precedence:** where a phase pins a temperature, the phase value overrides the persona's configured default (the persona default applies only when a phase leaves temperature open). An attached ExplorationPath stage overrides both. The resolved temperature for every LLM call is written to the `EventLog`. Phase temperatures below are stated as ranges; when a persona's configured default falls outside the range for that phase, the phase value wins.

### 13.1 Phase Definitions

#### Phase 1: Planning
- **Temperature:** Low (0.2). **Persona:** `tall` or `main`.
- Read task, project documents, acceptance criteria. Identify missing information. Propose implementation plan, tests, and documentation updates.

#### Phase 2: Divergence
- **Temperature:** High (0.7–1.1). **Personas:** `main`, `tall`, or `alternative`.
- Generate multiple candidate plans or implementations. Encourage different approaches. Avoid premature convergence.
- Default candidates: 3. For hard tasks: up to 10.

#### Phase 3: Evaluation
- **Temperature:** Zero (0.0). **Persona:** `security`.
- Check against acceptance criteria. Run tests in Job Pod. Run linters. Validate file paths and tool output. Compare candidates. Identify failures. Rank candidates.
- **Rule:** This phase is maximally deterministic.

#### Phase 4: Reflection
- **Temperature:** Moderate to high (0.6–0.8). **Persona:** `main`, `tall`, or `alternative`.
- Analyze why candidates failed. Identify missing constraints. Propose improvements and hybrid approaches. Ask whether a better method exists.

#### Phase 5: Synthesis
- **Temperature:** Low (0.2). **Persona:** `tall` or `main`.
- Produce final artifact with tests, documentation, change summary, and known limitations.

#### Phase 6: Comparison
- **Temperature:** Zero (0.0). **Persona:** `security`.
- Compare final artifact with previous best using acceptance criteria, test results, and the evaluation rubric.
- Output (structured JSON):

```json
{
  "winner": "new|previous|none",
  "confidence": 0.0,
  "reasons": [],
  "missing_requirements": []
}
```

#### Phase 7: Commit or Escalate
- **If accepted:** Write artifact to workspace. Create Git branch. Create atomic commit. Mark artifact as `accepted`. Request approval for external actions if needed.
- **If not accepted:** Store evaluation. Retry if budget remains. Otherwise escalate to HITL.

### 13.2 Exploration Paths

An `ExplorationPath` is a user-definable sequence of prompt and temperature stages that overrides the default Dialectical phases.

```yaml
id: uuid
name: string
description: string
stages:
  - name: divergence
    persona: alternative
    temperature: 1.1
    prompt_template: template_id
    max_tokens: int
  - name: evaluation
    persona: security
    temperature: 0.0
    prompt_template: template_id
    max_tokens: int
  - name: reflection
    persona: main
    temperature: 0.8
    prompt_template: template_id
    max_tokens: int
  - name: synthesis
    persona: tall
    temperature: 0.2
    prompt_template: template_id
    max_tokens: int
```

Exploration paths can be attached to projects or tasks, enabling custom workflows like "brainstorm-heavy" or "security-paranoid" modes.

### 13.3 Strategy Profiles & Outcome Records

Every job implicitly exercises a **strategy**: which personas ran which phases, at what temperatures, with which prompt templates, generating how many candidates, against what kind of task. Athanor records this explicitly so outcomes can accumulate into statistical evidence.

This system is deliberately **separate from the Feedback System (§18)**:

| | Feedback System (§18) | Strategy Analysis (§13.4) |
|---|---|---|
| Source | Human rejections/corrections and per-artifact failures | Aggregate outcomes across many jobs |
| Signal | Prescriptive — "do not do X in this artifact" | Descriptive — "approach Y is accepted 2× more often on code tasks" |
| Unit | `CorrectionRecord`, tied to an artifact | `StrategyInsight`, tied to a process pattern |
| Application | Injected directly into prompts (assembly position 8) | Advisory: biases defaults, ranks templates, proposes changes |

**StrategyProfile** — captured deterministically at job start from the persona plan (zero inference):

```yaml
id: uuid
job_id: uuid
project_id: uuid
archetype: inherited
exploration_path_id: optional
signature:
  - phase: diverging
    persona: alternative
    temperature: 0.9
    prompt_template: template_id
    candidates: 3
  # ...one entry per executed phase
task_features:
  task_type: implementation | refactor | test_writing | research | brainstorm
  difficulty_hint: from planning phase
```

**StrategyOutcome** — captured at job end, immutable once written:

```yaml
id: uuid
job_id: uuid
strategy_profile_id: uuid
result: accepted_new | accepted_previous | rejected | failed | cancelled
score: float
evaluator_confidence: float
retries: int
reflection_loops: int
token_cost: int
wall_time: duration
```

Both records are written transactionally with the corresponding state transitions and retained indefinitely — they are small, and they are the dataset everything in §13.4 learns from.

### 13.4 Strategy Analysis Engine

Aggregation is **deterministic** (grouping/ranking over `StrategyOutcome`s), run offline — primarily as the Daydreaming *Strategy Mining* action (§17.1). An optional Temp 0.0 `security` pass phrases detected patterns; it never invents numbers.

**Detection.** Outcomes are grouped by signature features (and feature pairs) within a scope (project or global), compared against the baseline for the same archetype/task mix:

```text
insight_candidate if
  cohort_jobs >= min_cohort_size
  AND |accept_rate - baseline_accept_rate| >= min_accept_rate_delta
  AND evaluator_confidence delta is directionally consistent
  AND no larger containing cohort contradicts it
```

*Winning*: higher accept rate and/or score — or equal quality at meaningfully lower retries/token cost. *Losing*: correlated with rejection, failure, loop alarms, or high retry counts. Losing insights are at least as valuable as winning ones; they justify earlier escalation and tighter budgets.

**StrategyInsight object:**

```yaml
id: uuid
scope: project | global
polarity: winning | losing
pattern:
  feature: diverging.persona
  value: alternative
  context: archetype=code
evidence:
  cohort_jobs: int
  accept_rate: float
  baseline_accept_rate: float
  median_score: float
  median_retries: int
  median_token_cost: int
statement: "Divergence led by 'alternative' doubled acceptance on code tasks."
status: proposed | active | muted | retired
created_at: timestamp
updated_at: timestamp
```

**Lifecycle:** `proposed` (mined; inert) → `active` (promoted via HITL confirmation, §20.1) → `muted` (user-suppressed; evidence kept) or `retired` (contradicted by newer evidence or expired). Auto-promotion of conservative insights is possible via configuration but defaults off.

**Application channels** (rivers, never mountains):

1. **Persona-plan defaults.** Active insights deterministically bias default persona/temperature selection for matching tasks; every bias application is logged to the `EventLog`.
2. **Template ranking.** Preferred prompt templates are ordered by insight evidence within each phase.
3. **ExplorationPath proposals.** Recurring winning signatures surface as draft `ExplorationPath` proposals for user review.
4. **Strategy notes in prompts.** A short, versioned runtime-tier line derived only from *active* insights (e.g., "on similar tasks, divergence led by `alternative` was accepted twice as often"), included in token accounting (assembly position 14).

**Guardrails:**
- Insights never modify static prompts, security policy, or evaluation rubrics — attempts trip the `drift` alarm (§22.3).
- Aggregation math is pure and unit-tested; the LLM only phrases results, at Temp 0.0.
- Every insight carries its evidence inline; the UI shows cohort size and deltas so users can judge for themselves.
- All computation is local. Nothing leaves the machine.

---

## 14. Coding Workflow

For code projects, Athanor defaults to test-driven execution.

```text
1. Read repository structure (wide persona).
2. Read documentation (wide persona).
3. Read acceptance criteria.
4. Generate or refine tests (tall persona, planning phase).
5. Generate implementation candidates (main/alternative, divergence phase).
6. Run tests in Job Pod (security persona, evaluation phase).
7. Run linters and formatters.
8. Compare results (security persona, comparison phase).
9. Update documentation (main persona, synthesis phase).
10. Create Git commit on agent-created branch.
11. Request approval for push if required.
```

**Required coding artifacts:** source changes · tests · test results (`EvaluationRecord`) · documentation updates · change summary · Git commit metadata.

**Coding context rules:**
- Use `wide` for repository ingestion and Dormant Index generation.
- Use `main` for normal implementation; `tall` for complex modules.
- Use `security` for evaluation and all compaction.
- Use `alternative` for second opinions on architecture.

---

## 15. Brainstorming Workflow

For exploratory work, Athanor generates and evaluates ideas without immediately writing code.

```text
1. Expand goal into questions and assumptions (tall persona).
2. Generate multiple directions (alternative persona, high temp).
3. Apply constraints (security persona, temp 0.0).
4. Score ideas against project goals (security persona, temp 0.0).
5. Select strongest ideas (main persona, reflection).
6. Produce proposal artifact (tall persona, low temp).
```

Output artifact:

```yaml
title: string
summary: string
assumptions:
  - assumption
questions:
  - question
options:
  - name: string
    description: string
    strengths: []
    weaknesses: []
    risks: []
recommendation: string
next_tasks:
  - task_definition
```

---

## 16. Research Workflow

Research tasks use the Gateway and Internet Gated Reader by default.

```text
1. Define research question.
2. Retrieve allowed sources (via Gateway, allowlist enforced).
3. Extract readable content (Reader Mode: HTTP fetch → readability → sanitize → markdown).
4. Summarize with citations (security persona, temp 0.0).
5. Identify contradictions (main persona, reflection).
6. Produce report artifact (tall persona, synthesis).
```

**Rules:**
- No JavaScript-heavy browsing by default.
- Reader Mode first. Zero JS execution.
- Browser Mode only with explicit HITL approval, in an isolated Job Pod.
- All retrieved documents pass through the Scanner (prompt-injection heuristics, malware scan).
- Source URLs and retrieval timestamps are stored in the `EventLog`.
- Default network policy: `deny`. Allowlist must be explicitly configured.

---

## 17. Daydreaming Engine (Idle-State Execution)

When the job queue is empty, the system is idle, and AC power is present, Athanor enters Daydream Mode, spawning low-priority background jobs.

### 17.1 Daydreaming Actions

| Action | Persona | Description |
|---|---|---|
| **Memory Consolidation** | `security` (temp 0.0) | Run Deterministic and Semantic Compaction on `episodic` and `archival` memory. Generate synopses. Update Dormant Index. |
| **Repository Exploration** | `wide` | Read repository files not yet indexed. Update vector embeddings in SQLite (sqlite-vec). Generate 1-line summaries for Dormant Index entries. |
| **Skill Refinement** | `main` | Review past `CorrectionRecord`s. Attempt to write new Python skills or update prompt templates to prevent past failures. **Must pass security scan. Must not modify static prompts.** |
| **Proactive Documentation** | `main` | Scan for undocumented functions (missing docstrings, missing README sections). Generate draft markdown documentation artifacts for user review. |
| **Feedback Review** | `security` (temp 0.0) | Analyze patterns in rejections and failures. Propose new global `CorrectionRecord`s derived from repeated project-level corrections. |
| **Strategy Mining** | `security` (temp 0.0) | Run deterministic aggregation over `StrategyOutcome`s (§13.4). Detect winning/losing patterns; create proposed `StrategyInsight`s. Read-only over history; produces proposals only. |

### 17.2 Daydreaming Constraints

- Daydreaming jobs run at lowest priority. Yield immediately if user becomes active or a real job is queued.
- Budget-limited: max 2 concurrent, max 30 minutes wall time each.
- Daydreaming jobs **cannot** create Git commits, push to remotes, or modify accepted artifacts.
- Daydreaming output is always `draft` status, requiring user review to promote.
- Disabled on battery power.

### 17.3 Daydream Log

```yaml
id: uuid
started_at: timestamp
finished_at: timestamp
action: memory_consolidation | repo_exploration | skill_refinement | proactive_documentation | feedback_review | strategy_mining
persona_used: persona
artifacts_produced:
  - artifact_ids (all draft status)
corrections_proposed:
  - correction_ids
insights_proposed:
  - strategy_insight_ids
memory_compacted:
  chunks_processed: int
  tokens_saved: int
dormant_index_entries_added: int
```

---

## 18. Feedback System

Feedback and strategy analysis (§13.4) are the twin mechanisms for self-improvement: this section captures **prescriptive** lessons about artifacts (mostly from humans); §13.4 mines **statistical** lessons about process (mostly from aggregate outcomes).

### 18.1 Feedback Sources

- User rejection of an artifact.
- User explicit correction.
- Test failure.
- Evaluator failure (`security` persona comparison).
- Security scan failure.
- Runtime error in Job Pod.
- Repeated loop detection (same tool call repeated N times).
- Budget exhaustion.
- Hallucinated path detection (referenced file does not exist).

### 18.2 CorrectionRecord Object

```json
{
  "id": "uuid",
  "project_id": "uuid or null",
  "job_id": "uuid or null",
  "artifact_id": "uuid or null",
  "scope": "project|global",
  "category": "architecture|style|testing|security|performance|tooling|documentation|other",
  "severity": "low|medium|high|critical",
  "user_feedback": "Do not use global state here.",
  "derived_rule": "Prefer explicit dependency injection over global state.",
  "applied_count": 0,
  "created_at": "timestamp",
  "updated_at": "timestamp"
}
```

### 18.3 Feedback Injection Rules

- High-severity corrections are injected before low-severity corrections.
- Project-scoped corrections outrank global corrections when relevant.
- Corrections are retrieved by vector similarity to the current task description.
- Corrections are summarized to reduce token usage (Temp 0.0, `security` persona).
- Users can mute, edit, promote, or delete corrections via the UI.

### 18.4 Mandatory Rejection Feedback

When a user rejects an artifact, the UI requires: category, severity, reason, desired behavior, and scope (project or global). This is fast but structured, ensuring every rejection produces a usable `CorrectionRecord`.

---

## 19. Evaluation System

The evaluator's main job is not to decide whether something is good in isolation, but whether it is **better than the alternative**.

### 19.1 Evaluation Inputs

Acceptance criteria · test results · linter results · documentation completeness · runtime errors · token cost · security scan results · prior user corrections · candidate artifacts.

### 19.2 Evaluation Output

```json
{
  "artifact_id": "uuid",
  "compared_against": "uuid or null",
  "score": 0.0,
  "passed_tests": true,
  "failed_tests": [],
  "missing_criteria": [],
  "security_issues": [],
  "style_issues": [],
  "better_than_previous": true,
  "confidence": 0.0,
  "summary": "string"
}
```

### 19.3 Comparison Rule

A new artifact becomes the best artifact only if:

```text
tests_pass_better_or_equal
AND acceptance_criteria_improve_or_equal
AND no_new_security_issue
AND evaluator_confidence > threshold
```

Where `threshold` is `execution.min_judge_confidence` in the configuration reference (§29).

---

## 20. Human-in-the-Loop (HITL) System

HITL is not a report. It is a live queue.

### 20.1 HITL Request Types

Approve external domain access · remote Git push · package installation · destructive operation · cloud inference usage · high compute budget · Browser Mode · resolve ambiguity (provide missing context) · provide missing credential through broker · accept or reject artifact · confirm project-scope correction · confirm global correction · activate strategy insight · mute strategy insight.

### 20.2 HITL Request Object

```yaml
id: uuid
job_id: uuid
task_id: uuid
project_id: optional
type: approval_type
severity: low | medium | high | critical
summary: string
details: string
actions:
  - approve
  - reject
  - modify
  - defer
created_at: timestamp
expires_at: optional
```

### 20.3 Live Watch Mode

The UI allows users to watch a job in real time:

- Token stream (live LLM output).
- Collapsible phase tree (current Dialectical phase highlighted).
- Tool call log (every `Action` logged with input/output).
- Test output (live from Job Pod).
- Artifact candidates (draft and candidate versions viewable).
- Evaluation scores (live `EvaluationRecord` updates).
- Pending approvals (HITL queue inline).
- Pause / Stop / Retry controls.
- Interruption Queue (add notes).

### 20.4 Interruption Queue

Users should not interrupt generation mid-token (this corrupts the context). Instead, user notes enter an `InterruptionQueue` and are injected at the next safe point:

- Next Dialectical phase transition.
- Next evaluation loop.
- Next context assembly.
- Next retry.

The `EventLog` records every injection.

---

## 21. Security Architecture

### 21.1 Rootless Podman

All Athanor containers run rootless. No privileged containers by default.

### 21.2 Job Pod Hardening

```text
--read-only-rootfs
--cap-drop=all
--security-opt=no-new-privileges
--security-opt=seccomp=<profile>
--tmpfs /tmp:rw,noexec,nosuid,nodev
--memory=<limit>
--cpus=<limit>
--pids-limit=<limit>
--network=none (default) or restricted internal
```

### 21.3 File Airlock

All files entering or leaving agent-managed workspaces pass through the Scanner.

**Ingress rules:**
- Resolve symlinks (reject if they escape workspace).
- Validate root paths.
- Copy content only (no metadata that could be malicious).
- Reject suspicious file types (executables, device files, setuid/setgid).
- Scan archives (zip bombs, path traversal).
- Scan for malware (ClamAV).
- Scan for prompt-injection patterns (YARA + security model).
- Quarantine failures.

**Egress rules:**
- Use `lstat` semantics (reject symlinks).
- Reject device files.
- Reject setuid/setgid files.
- Reject unexpected executables.
- Scan before export.

### 21.4 Prompt Injection Scanning

The Scanner inspects: uploaded documents (inbox), retrieved web content (from Gateway), user-imported prompts, skill manifests, plugin metadata.

**Detection methods:**
- Heuristic rules (known injection patterns).
- Small security-model classification (`security` persona, temp 0.0).
- Quarantine on uncertainty.

### 21.5 Internet Gated Reader (Network Gateway)

All external HTTP traffic goes through the Gateway.

**Gateway responsibilities:**
- Domain allowlist enforcement.
- Rate limiting.
- Request logging.
- Header sanitization (strip cookies, auth tokens, PII).
- Response size limits.
- Reader Mode extraction (HTTP fetch → readability → sanitize → markdown).
- Cloud inference mediation (if enabled; injects credentials into approved outbound requests only).

**Default network policy:**

```yaml
network:
  default_policy: deny
  allow_list: []
  rate_limit_per_minute: 30
  max_response_bytes: 10485760
```

### 21.6 Browser Mode (Restricted)

Browser Mode is optional and requires explicit HITL approval.

- Runs **only** inside an isolated Job Pod with `--network=none` except for a proxied connection to the Gateway.
- Use cases: JavaScript-required pages, authenticated flows under user supervision.
- Time-limited. Automatically terminated after timeout.

### 21.7 Credential Handling

Credentials are **never** passed directly into Job Pods as raw environment variables.

**Preferred model:**
- Credential broker on Core or host.
- Gateway injects credentials into approved outbound requests only.
- Job Pod never sees the secret.

### 21.8 UI & API Access

The UI binds to localhost only. For headless/remote access, use SSH port forwarding rather than exposing a network listener. The Core internal API (used by Job Pods) authenticates via per-job ephemeral tokens as described in §3.2.

---

## 22. Kill Switch and Alarms

### 22.1 Kill Switch

A persistent stop control must be available (UI button + CLI command).

**Actions:**
- Stop all Job Pods.
- Cancel active inference calls.
- Cancel pending fetches.
- Persist current state to SQLite.
- Enter frozen mode (no new jobs, no Daydreaming).

### 22.2 Exiting Frozen Mode

Frozen mode never lifts automatically. Unfreezing requires explicit user acknowledgment through the UI or CLI (`athanor unfreeze`), which records the acknowledgment and reason in the `EventLog`. On unfreeze, interrupted jobs resume from their last committed checkpoints.

### 22.3 Alarm Categories

| Alarm | Meaning | Default Level |
|---|---|---|
| `loop` | Same tool call or output repeats N times | `alert` |
| `resource` | Memory, CPU, disk, or token budget exceeded | `alert` |
| `security` | Suspicious file, prompt, or network behavior | `critical` |
| `quality` | High rejection rate (>50% of last 10 jobs) | `warning` |
| `stuck` | No progress for configured duration | `alert` |
| `hallucination` | Referenced files, symbols, or sources do not exist | `warning` |
| `budget` | Token or cost budget exceeded | `alert` |
| `self_modification` | Attempt to modify protected system components | `critical` |
| `drift` | Attempt to modify static security constraints | `critical` |

**Alarm levels:**
- `notice`: Logged, no action.
- `warning`: Logged, UI notification.
- `alert`: Pauses current job.
- `critical`: Freezes entire system, requests user intervention.

---

## 23. State, Persistence, and Recovery

### 23.1 SQLite Configuration

```sql
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
```

### 23.2 Stored State

SQLite stores:
- Projects, Goals, Tasks, Jobs, Actions, Artifacts.
- Prompt templates, Context bundles, Dormant Index entries.
- Evaluation records, Correction records.
- Preferences (project and global).
- HITL requests, Event logs.
- Token usage, Network rules.
- Model persona assignments, Configuration metadata.
- Strategy profiles, outcomes, and insights.
- Daydream logs.

### 23.3 State Persistence Rules

State is serialized to SQLite:
- After every state-machine node transition.
- Before and after job phase transitions.
- After artifact creation.
- After evaluation.
- After user approval or rejection.
- Before sleep.
- Before shutdown.
- Before migration.

### 23.4 Backups

```yaml
backup:
  auto: true
  schedule: "0 3 * * *"
  max_local_backups: 10
  include_workspace_metadata: true
```

Backups include: SQLite database, configuration, prompt templates, feedback records, workspace metadata. May exclude large binary artifacts if configured.

### 23.5 Migrations

- Versioned.
- Forward-only (no destructive down migrations).
- Embedded in binary.
- Backup before migration.
- Rollback only by restoring backup.

### 23.6 Recovery From Power Loss

- SQLite state is recovered from WAL.
- Incomplete job is flagged `interrupted` (a recovery flag on the Job record, §8.3 — not a state-machine state; the job retains its last committed state).
- Partial artifacts are marked `quarantined` or `draft`.
- Job resumes from last committed checkpoint on next startup.
- If no checkpoint is valid, job restarts with a recorded reason in `EventLog`.

---

## 24. Power and Idle Policy

```yaml
power:
  require_ac_for_deep_work: true
  battery_pause_threshold_percent: 20
  idle_resume_after: 5m
  allow_battery_override: false
  pause_on_sleep: true
  resume_on_wake: true
  daydream_on_idle: true
```

| Condition | Action |
|---|---|
| AC power present and system idle | Resume or continue deep work / Daydreaming |
| Laptop on battery | Pause deep work |
| Battery below threshold | Save state and pause |
| User active | Reduce resource usage (throttle inference) |
| User idle for configured period | Resume full background execution |
| System sleep | Flush state and pause containers gracefully |
| System wake | Resume from checkpoint |

---

## 25. Tool Interface

Tools are constrained, audited, and available only to Job Pods and the Core orchestrator.

| Tool | Description | HITL Required |
|---|---|---|
| `read_file(path)` | Read approved workspace file | No |
| `write_file(path, content)` | Write approved workspace file | No |
| `list_files(path)` | List approved directory | No |
| `search_files(pattern)` | Search workspace content | No |
| `execute_code(language, code)` | Execute inside Job Pod | No |
| `run_tests(command)` | Run project tests in Job Pod | No |
| `git_operation(repo, op)` | Local Git operations (branch, commit) | No |
| `git_push(repo, remote)` | Push to remote | Yes |
| `fetch_url(url)` | Fetch through Gateway (Reader Mode) | No (if domain allowlisted) |
| `search_web(query)` | Search through Gateway | No (if search engine allowlisted) |
| `browser_mode(url)` | Full browser in isolated Job Pod | Yes |
| `query_memory(query)` | Hybrid vector + FTS retrieval from SQLite | No |
| `context_swap(target_chunk_id)` | Swap dormant full-fidelity chunk into active context | No |
| `create_artifact(type, content)` | Create versioned artifact | No |
| `request_approval(type, details)` | Create HITL request | N/A |
| `add_correction(category, reason)` | Create `CorrectionRecord` | No |
| `install_package(name)` | Install system or language package | Yes |

---

## 26. Skills and Extensibility

Custom jobs and extensions are Python-first.

### 26.1 Skill Object

```yaml
id: uuid
name: string
version: string
description: string
runtime: python
entrypoint: module:function
dependencies:
  - python_packages
permissions:
  - network
  - filesystem
  - subprocess
prompt_template: optional
oci_artifact: optional
```

### 26.2 Skill Rules

- Skills are versioned.
- Skills are packageable as OCI artifacts or local directories.
- Skills declare permissions explicitly.
- Skills are mounted read-only into Job Pods.
- Skills do not access credentials directly.
- Skills must pass Scanner inspection before mounting.
- Skill prompts are versioned with the skill.

Skills return structured results:

```json
{
  "status": "success",
  "artifacts": [],
  "logs": [],
  "metrics": {},
  "errors": []
}
```

---

## 27. User Experience

### 27.1 Primary Views

**Dashboard:** Active jobs (with phase indicator), pending HITL approvals, failed jobs, recently completed artifacts, resource usage (RAM/VRAM/CPU), token usage (by persona/project), power state, active alarms, Daydreaming status.

**Watch View:** Live token stream, collapsible phase tree, tool call log, test output, evaluation scores, candidate artifacts with diff view, interruption queue, pause/stop/retry controls.

**Approvals:** Queue of pending HITL requests showing project, task, job, risk level, reason, requested action — with approve/reject/modify controls.

**Projects:** CRUD for settings, goals, documents, acceptance criteria, preferences. Task DAG visualizer. Versioned artifact history with status badges. Editable/mute-able correction records.

**Artifacts:** Versioned output browser. Revision diffs. Accept/reject (triggers structured feedback form). Open in workspace. View Git commit and `EvaluationRecord`. Export.

**Memory Browser:** Long-term memory entries, synopses, pinned context, Dormant Index viewer, compaction layers, correction records, retrieval test tool (query memory, see what comes back).

**Network Log:** Allowed/denied/rate-limited requests, domain approvals, Reader Mode extraction status.

**Security View:** Quarantined files, scan failures, prompt-injection detections, alarm history, kill switch, sandbox status.

**Statistics:** Approval rate, rejection rate and categories, token usage by persona/project, job success rate, test pass rate, average job duration, budget usage, Daydreaming productivity (artifacts proposed, corrections derived, memory compacted), strategy win rates by persona/phase/archetype, active strategy insights with inline evidence.

**Settings:** Models and personas, context minimums, power policy, network allowlist, backup schedule, cloud fallback, token budgets, telemetry preferences, workspace paths, security level, Daydreaming config.

### 27.2 Morning Digest

An asynchronous dashboard summarizing overnight activity:
- Completed DAGs and tasks.
- Generated artifacts (draft and accepted).
- Failed jobs (with reasons).
- Pending HITL requests.
- Daydreaming output (documentation drafts, proposed corrections, memory consolidated).
- Token usage summary.

---

## 28. Observability

### 28.1 Event Log Categories

Athanor logs structured JSON events to SQLite and `state/logs/`:

| Category | Events |
|---|---|
| `jobs` | State transitions, phase changes, retries, completions, failures |
| `recovery` | Checkpoint saves, crash recovery, interrupted job handling |
| `alarms` | Loop detection, resource limits, security flags, hallucination |
| `airlock` | Ingress/egress scans, quarantines, prompt-injection detections |
| `network` | Allowed/denied requests, rate limits, reader extractions |
| `podman` | Job Pod creation, teardown, socket access |
| `inference` | Model loads, context calculations, KV cache pressure, latency warnings |
| `feedback` | Correction creation, injection, applied counts |
| `strategy` | Profile/outcome capture, insight generation, promotion/expiry/muting, application decisions |
| `context` | Division events, compaction events, swap events, Dormant Index updates |
| `daydream` | Idle start/stop, actions taken, artifacts produced |
| `power` | AC/battery transitions, sleep/wake, throttle events |
| `backup` | Backup creation, migration execution |

### 28.2 Token Accounting

Local token accounting includes:
- Prompt tokens (per job, per persona, per project).
- Completion tokens.
- Total tokens.
- Estimated cloud cost (only if cloud enabled).
- Budget cutoffs and usage percentages.
- Usage by project.
- Usage by persona.
- Daydreaming usage (tracked separately for cost-benefit analysis).

---

## 29. Configuration Reference

Complete reference `config.yaml`:

```yaml
version: 2

agent:
  active_hours: "00:00-24:00"
  idle_threshold: 5m
  max_proactive_tasks_per_day: 10

power:
  require_ac_for_deep_work: true
  battery_pause_threshold_percent: 20
  idle_resume_after: 5m
  allow_battery_override: false
  pause_on_sleep: true
  resume_on_wake: true
  daydream_on_idle: true
  daydream_max_concurrent: 2
  daydream_max_wall_time_minutes: 30

inference:
  default_backend: ollama
  ollama_url: "http://host.containers.internal:11434"
  cloud_enabled: false
  cloud_requires_approval: true

personas:
  wide:
    model: "qwen2.5:7b"
    context_target: 65536
    temperature: 0.7
  tall:
    model: "qwen2.5-coder:32b"
    context_target: 16384
    temperature: 0.2
  main:
    model: "mistral-nemo:12b"
    context_target: 32768
    temperature: 0.4
  security:
    model: "phi3:3.8b"
    context_target: 8192
    temperature: 0.0
  alternative:
    model: "llama3.1:8b"
    context_target: 32768
    temperature: 0.8

context_engine:
  # Floors are role-scoped working-set requirements, not per-task constants (§12.6).
  coding_floor: 32768
  research_floor: 32768
  document_floor: 16384
  simple_floor: 8192
  compaction_temperature: 0.0
  enable_lossless_swapping: true
  kv_cache_warning_threshold: 0.85
  kv_cache_critical_threshold: 0.95

execution:
  divergence_candidates: 3
  max_hard_task_variations: 10
  judge_persona: security
  # The three flags below (require_tests_for_code,
  # require_documentation_for_code, compare_before_accept)
  # are M3-deferred: declared and defaulted to true, but the
  # engine does not yet consult them. Operators who set any
  # of these to false today will see no behavior change. They
  # become effective in M6/M7. See ROADMAP §7.
  require_tests_for_code: true
  require_documentation_for_code: true
  compare_before_accept: true
  min_judge_confidence: 0.7   # comparison confidence threshold in §19.3
  # Wall-time budgets are enforced per Dialectical phase (planning, diverging,
  # evaluating, reflecting, synthesizing, comparing). A single global job timeout
  # cannot fairly cover both a quick reflection and a full test-suite run in a
  # Job Pod; per-phase defaults are derived from these budgets.
  phase_wall_time_budgets:
    planning: 120s
    evaluating: 600s        # must accommodate Job Pod boot + test suite
    default: 300s

strategy_analysis:
  enabled: true
  min_cohort_size: 20
  min_accept_rate_delta: 0.15
  auto_promote: false            # promote conservative insights without HITL
  max_active_insights: 10
  insight_expiry_days: 90
  strategy_notes_in_prompts: true

limits:
  max_concurrent_jobs: 2
  max_concurrent_llm_calls: 1
  max_concurrent_fetches: 5
  max_tasks_per_hour: 20
  max_total_recoveries_per_job: 8

recovery:
  max_tool_call_retries: 2
  max_loop_interventions: 1
  max_context_compactions: 3

network:
  default_policy: deny
  allow_list: []
  rate_limit_per_minute: 30
  max_response_bytes: 10485760
  reader_mode_default: true
  browser_mode_requires_approval: true

security:
  scan_ingress_files: true
  scan_egress_files: true
  prompt_injection_scan: true
  quarantine_suspicious_files: true

backup:
  auto: true
  schedule: "0 3 * * *"
  max_local_backups: 10
  include_workspace_metadata: true

logging:
  level: info
  categories:
    - jobs
    - recovery
    - alarms
    - airlock
    - network
    - podman
    - inference
    - feedback
    - strategy
    - context
    - daydream
    - power
    - backup
```

---

## 30. Installation and Onboarding

### 30.1 Prerequisites

**Required:**
- Podman (rootless mode capable).
- Ollama or another inference endpoint.
- Sufficient disk space.
- Git.

**Optional:**
- Python (for skills development).
- ClamAV (for file scanning).
- Language-specific toolchains (for Job Pods).
- Media tools (for media archetypes).

> **macOS:** Podman runs inside a VM (`podman machine`). Athanor's memory budgeting and sleep/wake coordination account for the VM automatically; expect somewhat higher baseline overhead than on Linux.

### 30.2 First-Run Doctor

```bash
athanor doctor
```

**Checks:**
- Podman installed and rootless mode available.
- Podman socket reachable.
- Ollama reachable.
- Available RAM and VRAM.
- Disk space.
- Filesystem locking support.
- Git installed.
- Workspace permissions.
- Power policy.
- Network configuration.
- Model availability (per persona) — with remediation: if a persona model is missing, Doctor offers to run the equivalent `ollama pull` command.
- Context feasibility (can hardware meet context floors?).

Doctor proposes fixes where possible.

### 30.3 First Project

The first-run flow does not open a generic chat. It creates a project.

```yaml
name: unique
goal: "One sentence describing the objective."
archetype: text | code | document | data | media
initial_documents: optional
acceptance_criteria: optional
start_immediately: boolean
```

After creation, the user can:
- Start immediately and watch execution.
- Queue for later.
- Create additional projects.

### 30.4 Cloud Warning

If the user configures a cloud endpoint:

```text
Cloud inference is optional.
Athanor is optimized for local iterative execution.
Cloud calls may increase cost quickly, especially during multi-pass evaluation.
Enable cloud usage only as a fallback or for explicitly approved tasks.
```

Requires explicit confirmation.

---

## 31. Testing Strategy

### 31.1 Unit Tests
- State machine transitions.
- DAG decomposition and cycle detection.
- MCE KV-cache math and dynamic trigger logic.
- Temp 0.0 compaction determinism (same input → same output).
- Prompt assembly order and token accounting.
- Feedback injection ordering by severity and scope.
- Configuration parsing and validation.
- Strategy aggregation determinism and insight lifecycle transitions.

### 31.2 Integration Tests
- Podman Job Pod creation and strict teardown validation.
- Workspace mounting and Git atomic commit verification.
- Gateway allowlist enforcement and Reader Mode markdown extraction.
- Scanner quarantine behavior for symlinks, executables, and path traversal.
- SQLite WAL persistence and recovery after simulated crash.
- `context_swap` tool: verify chunk loaded matches original byte-for-byte.

### 31.3 Security Tests
- Symlink escape attempts.
- Path traversal payloads.
- Prompt-injection payloads in uploaded documents.
- Podman socket misuse attempts from Job Pods.
- Credential leakage detection (verify secrets never appear in Job Pod environment).
- Kill switch activation and frozen mode enforcement.

### 31.4 End-to-End (E2E) Tests
- **Code Project:** Submit a goal to implement a Python REST endpoint. Verify: DAG decomposition → test generation → implementation candidates → test execution → evaluation → Git commit → artifact accepted. All without user intervention.
- **Daydreaming:** Trigger idle state. Verify: memory compaction runs → repository indexed → documentation draft generated → DaydreamLog persisted.
- **Recovery:** Kill Core Pod mid-job. Restart. Verify: job resumes from last checkpoint → partial artifacts quarantined → no state loss.
- **Context Swap:** Submit a task requiring a large repository. Verify: Dormant Index generated → LLM issues `context_swap` → correct chunk loaded byte-for-byte → LLM continues execution.
- **Feedback Loop:** Reject an artifact with a structured reason. Verify: `CorrectionRecord` created → injected into next job → LLM avoids the identified mistake.
- **Strategy Analysis:** Run enough synthetic jobs with known outcomes to cross `min_cohort_size`. Verify: profile + outcome captured for every job → insight proposed at threshold → proposed insight does not affect prompts → approved insight biases the next persona plan (logged in EventLog).

---

## 32. Deployment

Athanor is deployed via a single CLI binary (`athanor`) which:

1. Bootstraps the Host Adapter (OS-level bridge).
2. Provisions the rootless Podman environment.
3. Pulls OCI images for the Core Pod and Job Pod base images.
4. Runs `athanor doctor` to validate hardware and prerequisites.
5. Starts the Core Pod.
6. Opens the local UI (or provides CLI interface for headless deployments).

**Headless deployment:**
- Host Adapter is optional.
- Core Pod runs directly via systemd or launchd.
- UI is accessed via localhost port forwarding (SSH).

**Update process:**
- Pull new OCI image.
- Run migrations (forward-only, embedded in binary).
- Restart Core Pod.
- Resume from checkpoint.
