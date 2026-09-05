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

### What works today (M0 + M1 + M2 + M3 + M4-T1 + M4-T2 + M4-T3 + M4-T4 + M4-T5)

- **Daemon on loopback** (`http://127.0.0.1:7420`). Config falls back to built-in defaults when `config.yaml` is absent; missing config is not an error on a fresh clone. `/healthz` reports status, version, uptime.
- **CLI client.** `project create`, `goal submit`, `job watch`, `artifacts`, `freeze`, `unfreeze`. Talks to a running daemon over loopback.
- **Walking-skeleton pipeline.** Submit a goal → queued → context_building → planning → diverging → evaluating → (reflecting) → synthesizing → comparing → completed. Draft artifacts persist under `state/artifacts/` with SHA-256 content hashes; the supersede chain is linear.
- **Dialectical engine (M3-T1 + M3-T2).** N-candidate divergence (default 3) feeds the evaluation phase, which runs the security persona at Temperature 0.0 with a per-archetype §19.1 rubric (`internal/engine/rubric.go`) and persists one `EvaluationRecord` per candidate. The §19.3 deterministic guard (`DecideWinner` in `internal/engine/decide.go`) downgrades an LLM `winner: new` verdict when no record meets `better_than_previous + confidence > min_judge_confidence`. Per-phase wall-time budgets emit a `context_deadline_exceeded` audit row on timeout.
- **Append-only audit log.** Every state transition writes a `jobs` event in the same transaction as the state update. `GET /jobs/{id}/events` returns the full chain.
- **Kill switch.** `POST /freeze` freezes; `DELETE /freeze` unfreezes with a required reason. Frozen state persists in `system_state` and survives restarts.
- **Crash recovery.** `kill -9` mid-job leaves a usable database; restart resumes from the last committed phase.
- **Container spine.** `internal/jobpod` owns the Podman Job Pod lifecycle (Start/Stop/Get/Sweep), platform-split hardening flags (Linux seccomp, Darwin no-op), per-job token bind mount. Wired into daemon boot for a startup sweep of orphan pods.
- **Internal API behind bearer tokens.** Job Pods authenticate to the daemon at `/internal/v1/` (job context, heartbeat, log, `execute_code`, `run_tests`, `lint`) with a 16-byte random hex token bound to their job ID. Gate G2 (`internal/gate/gate_g2_test.go`) structurally proves every route is wrapped in `authMiddleware` and every tool handler consults the per-job envelope. The closed tool set is `execute_code`, `run_tests`, `lint` (M2-T4 + M3-T2 commit 2.3).
- **Tool envelope.** For the `code` archetype, `phaseEvaluate` runs the LLM's proposal in a Job Pod (`execute_code` → `run_tests` → `lint` envelope), persists a `code` artifact with exit code, stdout, stderr, and duration, and folds the test/lint result into the verdict. Disallowed tools are rejected with 403 and audited; the engine treats a `tool_disallowed` as a soft-fail and M3-T3 will turn soft-fails into HITL escalations.
- **Containment guarantee (Gate G1).** An AST-walking test in `internal/gate/gate_test.go` fails the build if any production source imports a tool-execution capability. The M1 agent is provably LLM + storage only. See [docs/demo-m1.md](docs/demo-m1.md).
- **Path containment library (M4-T1).** `internal/airlock/paths` is the §21.3 file-airlock path-containment layer every file operation routes through. Three defenses in depth: `Resolve` rejects absolute paths, traversal (`../`), and NULL bytes; `Validate` rejects device / FIFO nodes, setuid / setgid bits, symlink escapes, and unexpected executables; `OpenNoFollow` issues the open with `O_NOFOLLOW` so the kernel refuses a symlink at the final component (closing the Lstat→open TOCTOU window). The `O_NOFOLLOW` constant is the only `syscall` identifier reachable in `internal/`, and it's reached only through build-tag-gated wrappers (`paths_linux.go`, `paths_darwin.go`) that Gate G1's rule 5 allowlists. Adversarial-corpus tests cover every rejection class; a `testing/quick` property test (200 random samples) asserts every unsafe (root, rel) input returns `ErrAbsolute` or `ErrTraversal`; an `O_NOFOLLOW` behavioral test verifies the kernel defense end-to-end (skips on GOOSes without `O_NOFOLLOW`).
- **Ingress pipeline (M4-T2).** `internal/airlock/ingress` watches `<state>/workspace/inbox/` with `fsnotify`, routes new files through `airlock/paths` + the per-pipeline scanner registry, and disposes to `.processed/` or `quarantine/`. The `quarantined_files` table (migration 0008) records every quarantine with the deciding scanner's reason; clean files are audited under `category=airlock, event=accepted`. Scanner-absent degrades to `VerdictUncertain` at scan time (fail-closed; the `cmd/athanor/scanners/` ClamAV / YARA adapters are external and may not be installed). The watcher is bound to the daemon's lifetime; deferred `Close` drains the pending queue before the process exits.
- **Pluggable scanners (M4-T3).** `internal/airlock/scanner` owns the `Scanner` interface (pure, streaming-aware, explicit verdict: `Clean` / `Uncertain` / `Rejected`) and the per-pipeline registry. In-tree scanners: `size` (configurable byte cap), `zipbomb` (decompression ratio + entry count), `heuristic` (prompt-injection keyword scan on long user prompts only). The worst-wins aggregation (`Rejected > Uncertain > Clean`) is the registry's contract; the egress pipeline intentionally omits the prompt-injection scanner because LLM-generated data is not adversarial to itself. Gate G1's allowlist extends to `cmd/athanor/scanners/` so the ClamAV / YARA adapters can shell out to their respective binaries; a future contributor cannot add a new `os/exec` site in `internal/` without tripping the gate.
- **Egress pipeline (M4-T4).** `internal/airlock/egress` subscribes to the append-only event log for `category=artifact, event=accepted` rows, runs the egress scanner registry (size + zipbomb + clamav + yara — no prompt-injection), validates the export tree through `airlock/paths`, and copies a clean export to `<state>/workspace/exports/<project>/<artifact>-<sha12>/`. The exporter is best-effort and asynchronous; a crash between event append and export means the next daemon boot re-exports on the next poll cycle (idempotent: the SHA-12 suffix is the content hash, so re-writing is a no-op). The same internal API (`/internal/v1/exports/<id>`) also serves the manual `athanor export` CLI subcommand synchronously.
- **Internet Gated Reader (M4-T5).** `internal/gateway` is the §21.5 *only* door between the Core and the public internet ([ADR-0017](docs/adr/0017-gateway.md)). The pure `Policy` decides: suffix-match allowlist with explicit `.` separator (so an allowlist of `wikipedia.org` does not match `evilwikipedia.org`), explicit-override `deny_list` (documented smell), DNS-rebinding guard refusing private/loopback/link-local/CGN/multicast/unspecified CIDR ranges via `net.DefaultResolver`, IDN fails closed. The `Client.Fetch` orchestrator layers on a per-host token-bucket rate limiter, a fixed deny-list header sanitizer (`Cookie`, `Authorization`, `Proxy-Authorization`, `X-Api-Key`, `X-Auth-Token`, `X-Secret` stripped by name; `User-Agent` set to a fixed Athanor UA), and a streamed response size cap that truncates at the cap and sets a `Truncated` flag (no failure). Every fetch appends one `network` event with the full outcome (`fetched` / `truncated` / `denied` / `private_ip` / `rate_limited` / `error`). The gateway is constructed at daemon boot via `cmd/athanor/gateway.go`; consumers (the M4-T7 tool envelope, the M6 credential broker) take the `Client` interface, not the concrete type, so a fake is substitutable in tests. ~30 `Test*` functions cover the policy corpus, the per-host rate limiter, the header sanitizer, the response cap, the pinned-IP dial (the §21.5 DNS-rebinding defense at the TCP layer), the `network` event shape, and the `NewClient` validation. Zero new dependencies; the project's dep list stays at two (`mattn/go-sqlite3`, `gopkg.in/yaml.v3`).
- **Container security suite (Gate G2, M2-T6).** A structural argv regression test in CI (`TestGateG2JobPodArgvCannotEscape`) blocks `--net=slirp4netns`, `podman.sock`, and host-FS bind-mount sources from ever entering the `podman run` argv. Five behavioral probes in `internal/jobpod/security_test.go` (gated by `ATHANOR_RUN_INTEGRATION`) bring up real hardened pods and assert the network, Ollama, podman socket, host FS, and credentials are all denied at runtime. Reference run 2026-08-30: all five probes pass. See [docs/demo-m2.md](docs/demo-m2.md).
- **M1 quality probe** ran 2026-08-26 with `gemma4:12b-mlx`; 5/5 acceptance-criteria adherence across text/code/document archetypes. Findings: [docs/probes/m1-quality-probe.md](docs/probes/m1-quality-probe.md).
- **M3-T2 per-task probe** in [`spikes/m3-t2-probe/`](spikes/m3-t2-probe/) — 5 code-archetype goals covering every line in the §19.1 rubric (clean baselines prove the rubric does not fire spuriously; deliberately-broken goals prove it does). Findings land in `docs/probes/m3-t2-probe.md` after the probe runs.

### What's next

M3 (Dialectical Loop v1) and M4-T1 through M4-T5 (path containment, ingress pipeline, pluggable scanners, egress pipeline, Internet Gated Reader) are done. M4 (Airlock & Gateway) is in progress: T6 (Reader Mode extraction — `go-readability` + `bluemonday`), T7 (gateway-backed tools `fetch_url` / `search_web`), and T8 (adversarial security suite — traversal payloads, zip bombs, injection corpus, SSRF attempts) are next. See [`ROADMAP.md`](ROADMAP.md) for the full M4 breakdown. The M3-T7 quality-probe T-a/b/c measurement experiments and the M3-T5 git-on-accepted call site are tracked as M3 follow-ups in `ROADMAP.md` §7. Gate G3 closes after the M3-T6 crash-recovery E2E suite is green end-to-end (the unit-level coverage is in place; the integration-level gating against a live daemon lands in a follow-up commit). Architectural decisions for the M3-T2 through M4-T5 work are in [`docs/adr/0011`](docs/adr/0011-external-api-host-allowlist.md)–[`0017`](docs/adr/0017-gateway.md). Two `network` config flags (`reader_mode_default`, `browser_mode_requires_approval`) are declared + defaulted but the gateway does not yet consult them — see the inline comments in `config.example.yaml` for the deferral (mirrors the dormant-Execution-flags pattern from `f309500`).

### What's deferred

M4-T6 (Reader Mode), M4-T7 (gateway-backed tools), M4-T8 (adversarial security suite), the dormant `network` config flags (see above), M5 Multidimensional Context Engine (spike first, then lossless division + sqlite-vec + Temp 0.0 compaction), M6 Autonomy & Feedback (HITL queue, CorrectionRecord loop, Strategy mining, real OS watcher, cloud-credential broker for §21.7, Browser Mode runtime for §21.6), M7 Endurance & Release (24h soak, fresh-install demo, first installable release). Items specifically deferred from M3-T2 are listed in `ROADMAP.md` §7 (M3-T7-a/b/c measurement backlog, M3-T5 git tool, the parser-consolidation refactor for ADR-0012, the external-API Host-header middleware for ADR-0011). The M4-T5.4 follow-up (Gate G1 rule 6: AST walk forbidding `http.Get` / `http.Post` / `http.DefaultClient` outside `internal/gateway/`) ships with the T8 adversarial suite. See [ROADMAP.md](ROADMAP.md) for the full plan and exit gates.

## Documentation

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — the complete design: topology, object model, MCE, personas, dialectical engine, security, configuration reference, testing strategy.
- **[docs/](docs/)** — implementation notes ([SQLite setup](docs/sqlite-setup.md), [ADRs](docs/adr/)).
- **[spikes/](spikes/)** — throwaway validation code.

## License

Athanor is licensed under the [GNU Affero General Public License v3.0](LICENSE).
