<div align="center">

# ⚗️ Athanor

### An alchemical furnace for your thoughts and code.

**Local-first · Container-native · Semi-autonomous · Built to burn 24/7**

**License: [AGPL-3.0](LICENSE)**

</div>

---

An *athanor* is an alchemical furnace designed to burn continuously without interruption. Athanor is a local-first, container-native, semi-autonomous agent system that runs on your own hardware, works while you sleep, and never sends your data anywhere unless you explicitly allow it.

You give it a **goal**. It autonomously decomposes the goal into a Directed Acyclic Graph (DAG) of tasks, then executes those tasks through a **Dialectical Execution Engine** that generates multiple candidates, tests them in isolated containers, evaluates them deterministically, and keeps only the best result. A **Multidimensional Context Engine (MCE)** maintains full-fidelity memory across long sessions through lossless division and deterministic Temp 0.0 compaction. When idle, it *daydreams* — consolidating memory, indexing repositories, and proactively documenting code.

## Core Characteristics

| | |
|---|---|
| 🏠 **Local-First** | Ollama by default. Cloud inference is optional, budget-gated, and HITL-approved. |
| 🔁 **24/7 Endurance** | Survives sleep, power loss, Podman restarts, and Ollama restarts. Resumes from checkpoint. |
| 📦 **Container-Native** | Rootless Podman. A persistent Core Pod (control plane) plus ephemeral, hardened Job Pods for execution. |
| 🧠 **Multidimensional Context** | Lossless AST/structural division for code and data; deterministic Temp 0.0 compaction for logs and conversations. |
| 📈 **Self-Improving** | Structured `CorrectionRecord`s are harvested from every failure and rejection, injected into future prompts via vector similarity — plus automatic win/loss mining of execution strategies. |
| 💭 **Daydreaming** | Idle cycles consolidate memory, index repositories, and proactively document code — always as reviewable drafts. |
| 🔒 **Secure by Default** | Internet Gated Reader (no browser, no JavaScript), file airlock with ClamAV/YARA scanning, ephemeral hardened Job Pods. |
| ✅ **Goal-Oriented Validation** | Goals become tasks with explicit `AcceptanceCriteria`. Artifacts are validated before synthesis and compared against prior best before acceptance. |

## Key Concepts

| Concept | One-liner |
|---|---|
| **Dialectical Execution Engine** | Multi-phase, multi-temperature state machine: diverge → evaluate → reflect → synthesize → compare. Multiple candidates enter; the deterministically-judged best leaves. |
| **Multidimensional Context Engine (MCE)** | N-dimensional memory system. Code is divided losslessly by AST/structure; logs and conversations are compacted deterministically at Temp 0.0. |
| **Personas** | Models assigned to functional roles — `wide` (ingestion), `tall` (hard reasoning), `main` (execution), `security` (judging & all compaction), `alternative` (orthogonal perspective). |
| **Job Pods** | Ephemeral, rootless Podman containers with read-only rootfs, no network, and resource limits. One per execution, destroyed after. |
| **CorrectionRecords** | Structured negative feedback (category, severity, derived rule) that makes every rejection improve every future job. |
| **Strategy Insights** | Automatic win/loss analysis of execution strategies (personas, temperatures, candidate counts). Statistical evidence only; advisory until approved. |
| **Daydreaming** | Budget-limited idle-time jobs: memory consolidation, repo indexing, skill refinement, proactive documentation. Output is always draft status. |
| **HITL Queue** | Human-in-the-loop as a live approval queue, not a report. External, irreversible, or high-risk actions pause for you. |
| **File Airlock** | All files entering or leaving agent-managed space pass through symlink/traversal/malware/prompt-injection scanning. |

## Architecture at a Glance

```text
Host Adapter (power, sleep, idle)  →  Core Pod [core · gateway · scanner · ui]
                                          │
                    ┌─────────────────────┼─────────────────────┐
                    ▼                     ▼                     ▼
                 SQLite               Ollama               Job Pods
              (WAL + vectors)      (local inference)    (ephemeral,
                                                        sandboxed execution)
```

Full topology, isolation rules, and every subsystem are documented in [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Quick Start

### Prerequisites

**Required:**
- [Podman](https://podman.io/) (rootless mode capable)
- [Ollama](https://ollama.com/) with persona models pulled (see `personas:` in the config reference)
- Git

**Optional:**
- ClamAV (file scanning) and YARA (rule-based detection)
- Python (for skill development)
- Language toolchains for Job Pods; media tools for media archetypes

> **macOS note:** Podman runs inside a VM (`podman machine`) on macOS. Athanor's Host Adapter accounts for this overhead automatically.

### Install & First Run

> **Note:** Athanor is pre-MVP; the container packaging (`athanor doctor` /
> `athanor start` below) is the M7 target. Today the daemon runs directly
> from a clone with a real CLI:

```bash
make build
make run                                     # daemon on http://127.0.0.1:7420 (defaults, no config needed)
./bin/athanor project create -name demo -archetype text -goal "Write a short essay about why local-first software matters."
./bin/athanor goal submit -project <id> -goal "Summarize it in five bullet points."
./bin/athanor job watch -job <id>            # streams every phase; ends with the draft artifact
```

Full walkthrough (including the kill switch and crash recovery):
[docs/demo-m1.md](docs/demo-m1.md) — that script is the Gate G1 demo.

The eventual flow:

```bash
# 1. Validate your hardware and environment (proposes fixes where possible)
athanor doctor

# 2. Start the Core Pod
athanor start
```

Athanor does not open a generic chat. The first-run flow creates a **project**:

```yaml
name: my-rest-api
goal: "Build a small REST API for managing a personal book collection."
archetype: code            # text | code | document | data | media
acceptance_criteria:
  - "pytest passes with >90% coverage on new endpoints"
start_immediately: true
```

Then watch it work in real time from the Watch View, or queue it and check the Morning Digest later.

> ⚠️ **Cloud warning:** Cloud inference is optional. Athanor is optimized for local iterative execution; cloud calls can increase cost quickly during multi-pass evaluation. Enabling requires explicit confirmation.

## What It Feels Like

| Workflow | You provide | Athanor delivers |
|---|---|---|
| **Code** | A goal + archetype constraints | DAG of tasks → test-driven implementation candidates run in Job Pods → evaluated, compared, committed on an agent-created branch |
| **Brainstorm** | An exploratory goal | Scored option sets with strengths/weaknesses/risks, assumptions, questions, and recommended next tasks |
| **Research** | A question + allowlisted domains | Reader Mode extraction (no JS), cited summaries, contradiction analysis, report artifact |

## Status

### What works today (M0 + M1 + M2 + M3 + M4 + M5 + M6)

- **Daemon on loopback** (`http://127.0.0.1:7420`). Config falls back to built-in defaults when `config.yaml` is absent; missing config is not an error on a fresh clone. `/healthz` reports status, version, uptime.
- **CLI client.** `project create`, `goal submit`, `job watch`, `artifacts`, `freeze`, `unfreeze`, `doctor`. Talks to a running daemon over loopback, except `doctor` which inspects the local host directly.
- **Doctor (M7-T5).** `athanor doctor` checks rootless Podman, Git, Ollama reachability, every persona model (with `ollama pull` remediation), memory, disk, state-dir writability, the Job Pod image, §12.6 context feasibility, and the network posture — failing loudly on hard prerequisites and warning on advisories. [ADR-0050](docs/adr/0050-doctor.md).
- **Walking-skeleton pipeline.** Submit a goal → queued → context_building → planning → diverging → evaluating → (reflecting) → synthesizing → comparing → completed. Draft artifacts persist under `state/artifacts/` with SHA-256 content hashes; the supersede chain is linear.
- **Dialectical engine (M3-T1 + M3-T2).** N-candidate divergence (default 3) feeds the evaluation phase, which runs the security persona at Temperature 0.0 with a per-archetype §19.1 rubric (`internal/engine/rubric.go`) and persists one `EvaluationRecord` per candidate. The §19.3 deterministic guard (`DecideWinner` in `internal/engine/decide.go`) downgrades an LLM `winner: new` verdict when no record meets `better_than_previous + confidence > min_judge_confidence`. Per-phase wall-time budgets emit a `context_deadline_exceeded` audit row on timeout.
- **Append-only audit log.** Every state transition writes a `jobs` event in the same transaction as the state update. `GET /jobs/{id}/events` returns the full chain.
- **Kill switch.** `POST /freeze` freezes; `DELETE /freeze` unfreezes with a required reason. Frozen state persists in `system_state` and survives restarts.
- **Crash recovery.** `kill -9` mid-job leaves a usable database; restart resumes from the last committed phase.
- **Container spine.** `internal/jobpod` owns the Podman Job Pod lifecycle (Start/Stop/Get/Sweep), platform-split hardening flags (Linux seccomp, Darwin no-op), per-job token bind mount. Wired into daemon boot for a startup sweep of orphan pods.
- **Internal API behind bearer tokens.** Job Pods authenticate to the daemon at `/internal/v1/` (job context, heartbeat, log, `execute_code`, `run_tests`, `lint`, `fetch_url`, `search_web`, `context_swap`, `query_memory`) with a 16-byte random hex token bound to their job ID. Gate G2 (`internal/gate/gate_g2_test.go`) structurally proves every route is wrapped in `authMiddleware`, every tool handler consults the per-job envelope, and the gateway-tool, `context_swap`, and `query_memory` routes are registered. The closed tool set is `execute_code`, `run_tests`, `lint`, `git_operation`, `fetch_url`, `search_web`, `context_swap`, `query_memory` (M2-T4 → M5-T7); `git_operation` is allowlisted but has no route or handler yet (M6).
- **Tool envelope.** For the `code` archetype, `phaseEvaluate` runs the LLM's proposal in a Job Pod (`execute_code` → `run_tests` → `lint` envelope), persists a `code` artifact with exit code, stdout, stderr, and duration, and folds the test/lint result into the verdict. Disallowed tools are rejected with 403 and audited; the engine treats a `tool_disallowed` as a soft-fail and M3-T3 will turn soft-fails into HITL escalations. The dispatch is now live end-to-end (M2-T4b, [ADR-0024](docs/adr/0024-engine-pod-dispatch.md)): the engine starts a per-job Job Pod, the internal API execs the tool call into it, and the pod is destroyed at terminal state.
- **Containment guarantee (Gate G1).** An AST-walking test in `internal/gate/gate_test.go` fails the build if any production source imports a tool-execution capability. The M1 agent is provably LLM + storage only. See [docs/demo-m1.md](docs/demo-m1.md).
- **Path containment library (M4-T1).** `internal/airlock/paths` is the §21.3 file-airlock path-containment layer every externally-influenced file operation routes through ([ADR-0029](docs/adr/0029-file-io-containment-scope.md) records the scope). Three defenses in depth: `Resolve` rejects absolute paths, traversal (`../`), and NULL bytes; `Validate` rejects device / FIFO nodes, setuid / setgid bits, symlink escapes, and unexpected executables; `OpenNoFollow` issues the open with `O_NOFOLLOW` so the kernel refuses a symlink at the final component (closing the Lstat→open TOCTOU window). The `O_NOFOLLOW` constant is the only `syscall` identifier reachable in `internal/`, and it's reached only through build-tag-gated wrappers (`paths_linux.go`, `paths_darwin.go`) that Gate G1's rule 5 allowlists. Adversarial-corpus tests cover every rejection class; a `testing/quick` property test (200 random samples) asserts every unsafe (root, rel) input returns `ErrAbsolute` or `ErrTraversal`; an `O_NOFOLLOW` behavioral test verifies the kernel defense end-to-end (skips on GOOSes without `O_NOFOLLOW`).
- **Ingress pipeline (M4-T2).** `internal/airlock/ingress` watches `<state>/workspace/inbox/` with `fsnotify`, routes new files through `airlock/paths` + the per-pipeline scanner registry, and disposes to `.processed/` or `quarantine/`. The `quarantined_files` table (migration 0008) records every quarantine with the deciding scanner's reason; clean files are audited under `category=airlock, event=accepted`. Scanner-absent degrades to `VerdictUncertain` at scan time (fail-closed; the `cmd/athanor/scanners/` ClamAV / YARA adapters are external and may not be installed). The watcher is bound to the daemon's lifetime; deferred `Close` drains the pending queue before the process exits.
- **Pluggable scanners (M4-T3).** `internal/airlock/scanner` owns the `Scanner` interface (pure, streaming-aware, explicit verdict: `Clean` / `Uncertain` / `Rejected`) and the per-pipeline registry. In-tree scanners: `size` (configurable byte cap), `zipbomb` (decompression ratio + entry count), `heuristic` (prompt-injection keyword scan on long user prompts only). The worst-wins aggregation (`Rejected > Uncertain > Clean`) is the registry's contract; the egress pipeline intentionally omits the prompt-injection scanner because LLM-generated data is not adversarial to itself. Gate G1's allowlist extends to `cmd/athanor/scanners/` so the ClamAV / YARA adapters can shell out to their respective binaries; a future contributor cannot add a new `os/exec` site in `internal/` without tripping the gate.
- **Egress pipeline (M4-T4).** `internal/airlock/egress` subscribes to the append-only event log for `category=artifact, event=accepted` rows, runs the egress scanner registry (size + zipbomb + clamav + yara — no prompt-injection), validates the export tree through `airlock/paths`, and copies a clean export to `<state>/workspace/exports/<project>/<artifact>-<sha12>/`. The exporter is best-effort and asynchronous; a crash between event append and export means the next daemon boot re-exports on the next poll cycle (idempotent: the SHA-12 suffix is the content hash, so re-writing is a no-op). The same internal API (`/internal/v1/exports/<id>`) also serves the manual `athanor export` CLI subcommand synchronously.
- **Internet Gated Reader (M4-T5).** `internal/gateway` is the §21.5 *only* door between the Core and the public internet ([ADR-0017](docs/adr/0017-gateway.md)). The pure `Policy` decides: suffix-match allowlist with explicit `.` separator (so an allowlist of `wikipedia.org` does not match `evilwikipedia.org`), explicit-override `deny_list` (documented smell), DNS-rebinding guard refusing private/loopback/link-local/CGN/multicast/unspecified CIDR ranges via `net.DefaultResolver`, IDN fails closed. The `Client.Fetch` orchestrator layers on a per-host token-bucket rate limiter, a fixed deny-list header sanitizer (`Cookie`, `Authorization`, `Proxy-Authorization`, `X-Api-Key`, `X-Auth-Token`, `X-Secret` stripped by name; `User-Agent` set to a fixed Athanor UA), and a streamed response size cap that truncates at the cap and sets a `Truncated` flag (no failure). Every fetch appends one `network` event with the full outcome (`fetched` / `truncated` / `denied` / `private_ip` / `rate_limited` / `error`). The gateway is constructed at daemon boot via `cmd/athanor/gateway.go`; consumers (the M4-T7 tool envelope, the M6 credential broker) take the `Client` interface, not the concrete type, so a fake is substitutable in tests. ~30 `Test*` functions cover the policy corpus, the per-host rate limiter, the header sanitizer, the response cap, the pinned-IP dial (the §21.5 DNS-rebinding defense at the TCP layer), the `network` event shape, and the `NewClient` validation. Zero new dependencies at T5; Reader Mode (M4-T6) later adds the extraction deps (`codeberg.org/readeck/go-readability/v2`, `bluemonday`) — see the Reader Mode bullet below.
- **Reader Mode extraction (M4-T6).** `internal/gateway/reader.go` implements §21.5 responsibility 6 on top of the T5 Client: a fetched HTML response goes through `readability.FromReader` → bluemonday `UGCPolicy` sanitization → an in-house `renderMarkdown` pass → a prompt-injection scan, and comes back as markdown ready for an LLM prompt ([ADR-0018](docs/adr/0018-reader-mode.md)). `text/plain` passes through verbatim (still scanned); images/PDFs and absent Content-Types are refused (`ErrNotReadable`); a JS-only page with no main content is `ErrNoReadableContent` (the raw HTML is *never* returned as a fallback); an injection hit is `ErrPromptInjection`. `network.reader_mode_default: true` is effective — set it `false` to disable extraction (`ErrReaderDisabled`). Two new `network` events, `reader_mode_applied` / `reader_mode_rejected`, are audited per extraction. The renderer maps the sanitized tree to a closed markdown subset (headings, paragraphs, nested lists, http(s)-only links, fenced/inline code, blockquote, hr, tables) — deliberately no third-party markdown lib. The extraction deps are `codeberg.org/readeck/go-readability/v2` v2.1.2 (the maintained continuation of the deprecated go-shiori module; see the ARCHITECTURE §2 note) and `github.com/microcosm-cc/bluemonday` v1.0.27. ~25 `Test*` functions cover the hostile script/iframe/object/form corpus, the occlusion corpus, the text/plain path, and a real httptest fetch→extract end-to-end.
- **Gateway-backed tools (M4-T7).** `fetch_url` and `search_web` join the closed tool envelope (per-task override only — the daemon default grants nothing). Both routes dispatch server-side through a `ToolGateway` adapter over the §21.5 Client + Reader; Job Pods run with `--network=none`, so the Core executes every fetch and the allowlist holds for every byte. ADR-0019 §3 closes a latent SSRF gap found during planning: redirects are now a bounded hop loop (max 5) inside `Client.Fetch` with per-hop policy re-evaluation, per-hop rate limiting, and per-hop pinned-IP dialing — an allowlisted host can no longer 302 the gateway anywhere. `search_web` is a configured `text/template` engine URL (`network.search_engine_url_template`, default empty → typed 501); the engine host gets no allowlist exemption. Tool responses are prompt material: raw bytes are never inlined (reader refusals degrade to `mode=raw` with empty markdown; injection-flagged content → 422), and a truncated extraction re-fetches once at a halved cap. The engine's research sub-step fetches the task's declared URLs before divergence and injects attributed markdown into every candidate (soft-fail per source; ADR-0019 §7 as amended). Demo: [docs/demo-m4-t7.md](docs/demo-m4-t7.md).
- **Adversarial security suite (Gate G4, M4-T8).** Four corpora attack the gateway and scanner and all run in CI: SSRF (every deny-listed CIDR at its boundaries, DNS rebinding across redirect hops, integer-host IP obfuscation refused at two layers, the production-wire guard regression ADR-0017 §4 promised), allowlist bypass (typosquat / userinfo / trailing-dot / scheme smuggling / CRLF / IDN — with positive controls pinning legitimate spellings, and per-denial `network` audit assertions), and content attacks (a 10-payload LLM-targeted injection corpus fail-closed through the Reader; a ~10 MiB gzip bomb truncated at the streamed cap; a redirect-loop bomb bounded by the hop cap). The corpus found and fixed two real scanner weaknesses on its first run (multiline flag on the role-reassignment pattern; base64 decode-evasion via chained padded blobs). Behavioral probes (gated `ATHANOR_RUN_INTEGRATION`) re-prove default-deny and allowlisted end-to-end fetch against the real internet. Demo: [docs/demo-m4-t8.md](docs/demo-m4-t8.md).
- **Container security suite (Gate G2, M2-T6).** A structural argv regression test in CI (`TestGateG2JobPodArgvCannotEscape`) blocks `--net=slirp4netns`, `podman.sock`, and host-FS bind-mount sources from ever entering the `podman run` argv. Five behavioral probes in `internal/jobpod/security_test.go` (gated by `ATHANOR_RUN_INTEGRATION`) bring up real hardened pods and assert the network, Ollama, podman socket, host FS, and credentials are all denied at runtime. Reference run 2026-08-30: all five probes pass. See [docs/demo-m2.md](docs/demo-m2.md).
- **M1 quality probe** ran 2026-08-26 with `gemma4:12b-mlx`; 5/5 acceptance-criteria adherence across text/code/document archetypes. Findings: [docs/probes/m1-quality-probe.md](docs/probes/m1-quality-probe.md).
- **M3-T2 per-task probe** in [`spikes/m3-t2-probe/`](spikes/m3-t2-probe/) — 5 code-archetype goals covering every line in the §19.1 rubric (clean baselines prove the rubric does not fire spuriously; deliberately-broken goals prove it does). Findings land in `docs/probes/m3-t2-probe.md` after the probe runs.
- **MCE division + chunk store (M5-T2).** `internal/mce/division` divides files losslessly: tree-sitter for Go/Python/JavaScript, structural headers for markdown/plain text, fixed-line fallback. The byte-partition property (P1–P5) is proven on 198 files across 5 languages — Go boundary agreement **1.0000/0.8872**, reproducing the M5-T1 spike. `internal/mce` persists dormant chunks in SQLite (migration 0009) with deterministic content-derived IDs and a Dormant Index; reassembly through the store is byte-identical, and ingestion reads only through the §21.3 path-containment library. Dormant Index summaries use the `wide` persona at temperature 0.0 through a `Summarizer` seam whose LLM adapter lives in `cmd/` (the MCE package stays LLM-free). **M5-T3 adds the active/dormant swap**: `context_swap` joins the closed tool set, `Swap` loads the requested chunk byte-for-byte and returns the replaced chunk intact, and the active pointer is persisted so a restart resumes the same working set. The route is envelope-gated and Core-executed, and `internal/mce` still does not import `internal/llm`. **M5-T4 adds the KV-cache monitor**: every engine call is assessed *before* it is sent — strictly-`>` 85%/95% thresholds against the persona's `num_ctx` — and the outcome is audited as a `kv_cache_pressure` row (category `inference`). `warn` records and proceeds; `critical` force-evicts through a nilable `Evictor` seam and pauses when nothing is evictable; a prompt that cannot fit the window pauses the job with the §12.3 recommendation instead of being sent — Ollama truncates silently at `num_ctx`, so this is where "never silently truncates" becomes enforced per call. **M5-T5 adds the §10.5 context assembly priority queue**: every §11.2 section is priced per tier, a strict bottom-up ladder (7→6→5→4) decides which tiers survive — tiers 1–3 can never be evicted — and the emitted order stays §11.2's. The budget is the §10.4 critical threshold applied to the persona window; evictions persist per job in `system_state` so a restart does not resurrect them, and the ladder is the real `Evictor` seam. The MCE working set now reaches the prompt: the §10.1 active chunk renders as §11.2 §7, the Dormant Index as §11.2 §10 with the `context_swap` invitation, and the §11.2 §2 tool text is envelope-aware (it lists exactly the tools a task's `allowed_tools` grant, so the prompt can never promise a tool the server would refuse). Decisions: [ADR-0020](docs/adr/0020-division-strategy.md), [ADR-0021](docs/adr/0021-mce-chunk-store.md), [ADR-0022](docs/adr/0022-kv-cache-monitoring.md), [ADR-0023](docs/adr/0023-context-assembly-priority.md).
- **MCE compaction, retrieval, and repository indexing (M5-T6/T7/T8).** Temp 0.0 compaction is content-addressed (migration 0012 — the same input resolves to the same memo with no model call) with the §10.3 treatment matrix and a minimal §17.1 memory-consolidation daydream driver (M5-T6 — **Gate G5 closed**). `query_memory` is live: FTS5 `bm25()` fused with an in-Go cosine vector index by reciprocal rank fusion, mandatory scope isolation, and the `sqlite_fts5` build tag with a loud boot preflight (M5-T7, [ADR-0026](docs/adr/0026-memory-retrieval.md)). The **repository indexing pipeline** (M5-T8, [ADR-0028](docs/adr/0028-repository-indexing.md)) is the producer that fills `memory_embeddings` and the chunk store in production: `athanor index` / `POST /projects/{id}/index` walk a project's `repository_path`, divide each file, refresh Dormant Index summaries, and embed a bounded digest — incrementally, so an unchanged repository makes **zero** model calls on a second pass. A repository chunk is reachable via `query_memory` → `context_swap`; the §17.1 Repository Exploration action runs it while idle. **Caveat (M3-T7 finding A):** repository chunks are project-attributed, while the engine's automatic Dormant Index is job-scoped, so repository context is reachable through the `query_memory` tool rather than appearing in the prompt's working set automatically — see [docs/f4-plan.md](docs/f4-plan.md). **M5 complete.**
- **DAG decomposition (M6-T1).** A goal can now be decomposed into a validated, dependency-ordered task DAG before execution ([ADR-0032](docs/adr/0032-dag-decomposition.md), [plan](docs/m6-plan.md)): the pure `internal/dag` package parses the `tall` persona's structured output and validates it deterministically (unique keys, resolved parent/dependency references, acyclicity with a topological order, and depth/node/leaf-coverage/budget bounds), while `internal/decompose` retries a parse or validation failure once with `main` and persists the accepted graph atomically through `project.CreateDAG` (no migration — the existing `tasks` columns carry it). `athanor goal decompose` and `POST /projects/{id}/decompose` expose it; `GET /projects/{id}/tasks` re-inspects a graph. **M6-T2 adds the dependency scheduler** ([ADR-0033](docs/adr/0033-dependency-scheduler.md)): migration 0017 installs the canonical §7.3 task lifecycle, `internal/scheduler` runs ready leaves in dependency order and blocks the descendants of a failure, and the daemon reconciles interrupted graphs at boot. `goal submit` decomposes-and-schedules only when `execution.dag_decomposition` is true (default off, preserving the single-task path). **M6-T3 adds the failure policy** ([ADR-0034](docs/adr/0034-task-failure-policy.md)): a failed task is retried up to `execution.max_task_retries` (or its own `budget.max_jobs`, whichever is tighter), then blocked — which blocks its descendants — after an attempt at re-decomposition and HITL escalation; a cancelled job is terminal. **M6-T4 adds the HITL queue** ([ADR-0035](docs/adr/0035-hitl-queue.md)): a job can park in `awaiting_approval` and resume exactly where it left off, `athanor hitl list|approve|reject|defer` (and `GET /hitl` / `POST /hitl/{id}/decision`) drive it, and an unanswered request is denied on its TTL (default 24h). **`git_push` is HITL-gated** ([ADR-0036](docs/adr/0036-hitl-git-push.md)): `athanor push` only files a request — the remote changes on approval, never on denial. **M6-T6 adds CorrectionRecords** ([ADR-0037](docs/adr/0037-correction-records.md)): rejections and failures are captured with the §18.4 mandatory form, listed, and muted; M6-T7 injects them into prompts. **M6-T7 adds feedback injection** ([ADR-0038](docs/adr/0038-feedback-injection.md)): active corrections are ranked severity→scope→recency and rendered into prompt assembly position 8, with every injection audited (ids + tokens) and applied counts tracked. **M6-T8 adds a local web UI** ([ADR-0039](docs/adr/0039-local-web-ui.md)) at `http://127.0.0.1:7420/ui`: a dashboard with live phases and approvals, projects/artifacts/corrections (with diffs and the §18.4 rejection form), and a watch view with streamed tokens, interruption notes, and pause/resume/cancel/retry. The feedback loop is proven end-to-end (M6-T9): a structured rejection's rule appears in the next job's prompt for that project. **M6-T10 captures strategy** ([ADR-0040](docs/adr/0040-strategy-capture.md)): every job records a profile (from its persona plan) at start and an immutable outcome at end, ready for M6-T11's aggregation. **M6-T11 analyzes strategies** ([ADR-0041](docs/adr/0041-strategy-analysis.md)): deterministic cohorts yield proposed winning/losing insights that stay inert until a HITL-approved promotion, surfaced via `athanor strategy` and the Statistics panel. **M6 complete.**

### What's next

M4 (Airlock & Gateway) is done — T1 through T8: path containment, ingress pipeline, pluggable scanners, egress pipeline, Internet Gated Reader, Reader Mode extraction, gateway-backed tools (`fetch_url` / `search_web`), and the adversarial security suite. **Gate G4 closed** (adversarial suite green; research goal end-to-end on allowlisted domains — [demo-m4-t7.md](docs/demo-m4-t7.md), [demo-m4-t8.md](docs/demo-m4-t8.md)). **M5 (Multidimensional Context Engine) is complete**: the division-strategy spike ([ADR-0020](docs/adr/0020-division-strategy.md)), the division engine + chunk store + Dormant Index (M5-T2), the active/dormant swap + `context_swap` tool (M5-T3 — Gate G5's byte-exact arms), the KV-cache monitor with its 85%/95% triggers and floor-breach pause (M5-T4), and the §10.5 context assembly priority queue with its 7→6→5→4 eviction ladder, persisted per-job suppression, and the live MCE working set in the prompt (M5-T5 — Gate G5's assembly-priority arm), and Temp 0.0 compaction with its content-addressed determinism, the §10.3 treatment matrix, migration 0012 (`compacted_memory`), and the minimal §17.1 memory-consolidation daydream driver (M5-T6 — **Gate G5 closed**), and hybrid `query_memory` retrieval (M5-T7 — FTS5 `bm25()` fused with vector cosine by reciprocal rank fusion, migration 0013, the `sqlite_fts5` build tag, and the §25 closed set at 8 tools), and repository indexing (M5-T8 — migration 0014, the incremental `indexed_sources` manifest, `athanor index` / `POST /projects/{id}/index`, and the §17.1 idle exploration action) are done; decisions in [ADR-0021](docs/adr/0021-mce-chunk-store.md), [ADR-0022](docs/adr/0022-kv-cache-monitoring.md), [ADR-0023](docs/adr/0023-context-assembly-priority.md), [ADR-0025](docs/adr/0025-compaction.md), and [ADR-0026](docs/adr/0026-memory-retrieval.md). The vector half is a `VectorIndex` seam with an in-Go cosine implementation; the native sqlite-vec `vec0` accelerator is deferred to M7 packaging (ADR-0026 §2). **Next: M7 (Endurance & Release)** — M6 (Autonomy & Feedback) is complete (T1–T11): DAG decomposition and the dependency scheduler, failure policies, the HITL queue, HITL-gated `git_push`, `CorrectionRecord` capture/injection, strategy capture/analysis, and the local web UI all landed. T8 is the producer the earlier M5 features retrieve from; project-scoped chunks do not yet enter the §11.2 §10 assembly automatically (that is M6 — ADR-0028 §7), but the model reaches them via `query_memory` → `context_swap`. The M3-T7 quality-probe T-a/b/c measurement experiments and the M3-T5 git-on-accepted call site are tracked as M3 follow-ups in `ROADMAP.md` §7. `reader_mode_default` (effective since M4-T6) and `search_engine_url_template` (inert-but-declared until set) are live; `browser_mode_requires_approval` remains declared + defaulted but the gateway does not yet consult it — see the inline comment in `config.example.yaml` for the deferral (mirrors the dormant-Execution-flags pattern from `f309500`).

### What's deferred

M4-T8 (adversarial security suite — done; see above), the dormant `browser_mode_requires_approval` flag (see above), the remainder of M6 Autonomy & Feedback beyond M6 (the T3b alternative-persona re-decomposition adapter, real OS watcher, cloud-credential broker for §21.7, Browser Mode runtime for §21.6, full §27 Statistics/Memory Browser views, live-model DAG/quality-probe runs), M7 Endurance & Release (24h soak, fresh-install demo, first installable release). Items specifically deferred from M3 are listed in `ROADMAP.md` §7 (the M3-T7-b/c quality-probe measurement backlog). The M3-T5 Git-as-undo call site landed in F3-T5 ([ADR-0030](docs/adr/0030-git-as-undo.md)). See [ROADMAP.md](ROADMAP.md) for the full plan and exit gates.

## Documentation

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — the complete design: topology, object model, MCE, personas, dialectical engine, security, configuration reference, testing strategy.
- **[docs/](docs/)** — implementation notes ([SQLite setup](docs/sqlite-setup.md), [ADRs](docs/adr/)).
- **[spikes/](spikes/)** — throwaway validation code.

## License

Athanor is licensed under the [GNU Affero General Public License v3.0](LICENSE).
