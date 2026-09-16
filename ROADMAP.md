# Athanor — Development Roadmap

**Status**: Living document | **Canonical design**: [ARCHITECTURE.md](ARCHITECTURE.md) | **Philosophy**: Vertical slices, each runnable, each demoable, each safe.

---

## Status

| Milestone | State |
|---|---|
| TASK-000 SQLite + sqlite-vec spike | ✅ Done (findings: [docs/sqlite-setup.md](docs/sqlite-setup.md)) |
| Power manager draft (`internal/power`) | ✅ Aligned with ARCHITECTURE §24 naming (jobs, not VMs) and fully unit-tested |
| M0 Foundations | ✅ Done — T1–T8 complete (module, config, logging, store, migrations, EventLog API, `/healthz`, CI) + enum-normalization migration 0003 ([ADR-0005](docs/adr/0005-canonical-enum-values.md)). Gate G0 passed: kill -9 crash/restart verified, migrations forward-only and idempotent |
| M1 Walking Skeleton | ✅ Done (T1–T8) — T1–T7 code-complete, Gate G1 evidence: [docs/demo-m1.md](docs/demo-m1.md), E2E + AST containment tests in CI. T8 quality probe ran on 2026-08-26 with `gemma4:12b-mlx` (5/5 samples met acceptance criteria, adherence 5/5); findings: [docs/probes/m1-quality-probe.md](docs/probes/m1-quality-probe.md). T8 follow-ups integrated 2026-08-26: M1-T8.1 (synthesis preamble suppression), M1-T8.2 (default planning budget 120s→300s), M1-T8.3 (synthesizing-phase atomicity recovery test), M1-T8.4 (power manager wired into engine concurrency cap) |
| M2 Container Spine | ✅ Done (T1–T6) — T1 (rootless podman spike, [ADR-0007](docs/adr/0007-podman-lifecycle.md)), T2 (Job Pod manager in `internal/jobpod`, production client in `cmd/athanor/jobpod_client.go`, sweep on daemon boot), T3 (per-job tokens + `/internal/v1/` API with bearer-token auth, [ADR-0008](docs/adr/0008-job-pod-tokens.md)), T4 (internal API `execute_code` + `run_tests` with per-job tool envelope enforced server-side; engine sub-steps in `phaseSynthesize` for `code` archetype; [ADR-0009](docs/adr/0009-engine-pod-wiring.md)), T5 (terminal-state cleanup + token-dir revocation on supervisor exit), T6 (security test suite: structural argv regression test in CI + five behavioral probes gated by `ATHANOR_RUN_INTEGRATION`, [ADR-0010](docs/adr/0010-m2-security-suite.md), [plan](docs/m2-t6-plan.md)). Gate G2 evidence: [docs/demo-m2.md](docs/demo-m2.md); Gate G2 fully green on 2026-08-30 (macOS 14 / podman 5.8.2 / applehv, all five behavioral probes pass; structural argv + middleware + envelope + LLM-isolation tests green in CI) |
| M3 Dialectical Loop v1 | ✅ Done (T1–T7) — T1 ([plan](docs/m3-t1-plan.md)) shipped the full §8 phase executor + EvaluationRecord schema + §19.3 deterministic guard in 4 commits (`c73a115`, `9e99e20`, `9cb4ccd`, `62ae865`); the E1/E2/E4/E5 dialectical-loop test suite landed with the close-out. T2 shipped in 6 commits (`a4e2362`–`97aed91`): M2-T4 pod sub-steps moved to `phaseEvaluate` (ADR-0014), the per-archetype §19.1 rubric drives per-candidate acceptance checks, the `/internal/v1/jobs/{id}/lint` route extends the tool envelope to a closed three-tool set, the §19.3 guard is a pure function (`DecideWinner`, `internal/engine/decide.go`), the per-phase wall-time budget emits a `context_deadline_exceeded` audit row, and `spikes/m3-t2-probe/` exercises 5 code-archetype goals. T3 shipped in 4 commits (`2fbae3c`–`a5da467`): the comparison prompt includes a "Previous-record summary" section, the supersede+accept transition is atomic via `SupersedeAndAccept` (§9.3 status flow gains the `accepted → superseded` edge), `parseComparisonVerdict` trims whitespace and reports unknown winners via a typed error (no more silent downgrades), and the `DecideWinner` table tests gain 6 new rows for ties and boundaries. T4 shipped in 3 commits (`6956a1f`–`dbe3478`+the cap-validation commit): the reflection counter is a typed `system_state` row (no more `jobs.recovery_flag` hack), the budget is a config field (`execution.max_reflection_loops`), and `validateCross` rejects non-positive values. T5 added `git_operation` to the closed tool envelope (the call site is T7 work). T6 added crash-recovery E2E at `StateDiverging` and `StateEvaluating` boundaries. T7 scaffolded the dialectical-vs-single-shot quality probe (`spikes/m3-t7-probe/`); the T-a/b/c measurement experiments land as follow-up work. Architectural decisions in [`docs/adr/0011`](docs/adr/0011-external-api-host-allowlist.md)–[`0014`](docs/adr/0014-evaluation-phase-move.md). `make check` clean; Gates G1 + G2 re-proven. |
| M4 Airlock & Gateway | ✅ Done (T1–T8) — T1 (`5f829b7` + Gate G1 rule 5 prep `54d8d7c`) shipped the §21.3 path containment library at `internal/airlock/paths`: `Resolve` (path arithmetic), `Validate` (mode + structural), `OpenNoFollow` (`O_NOFOLLOW` via build-tag-gated wrappers — the only `internal/` files that may import `syscall`). Property test (`testing/quick`, 200 iters) over the adversarial path corpus; round-trip test; behavioral `O_NOFOLLOW` test (skips on unsupported GOOSes); setuid/setgid rows self-skip on hosts whose filesystem drops the bit on `chmod`. T2 (`0f727b0`) shipped the ingress pipeline: `fsnotify` watcher on `<state>/workspace/inbox/`, per-pipeline scanner registry (size + zipbomb + heuristic), `quarantined_files` table (migration 0008), adversarial-corpus test. T3 (`28e53cc`) shipped the pluggable scanners: in-tree `size` / `zipbomb` / heuristic, `cmd/athanor/scanners/` for ClamAV + YARA adapters (Gate G1 allowlist extension for the `os/exec` directory), scanner-absent degrades to `VerdictUncertain` at scan time (fail-closed). T4 (`3b41b89`) shipped the egress pipeline: event-driven exporter on `category=artifact, event=accepted`, manual `athanor export` CLI subcommand, atomic temp+rename, SHA-12 idempotency. T5 (`ab082e8` + `225ed38`) shipped the §21.5 Internet Gated Reader per [ADR-0017](docs/adr/0017-gateway.md): `internal/gateway/` package owns the pure `Policy` (suffix-match allowlist with explicit `.` separator, deny-list override, non-routable CIDR guard, IDN fails closed), per-host token-bucket rate limiter, fixed deny-list header sanitizer, streamed response size cap (truncate + flag, do not fail), `network` event log. ~30 `Test*` functions in `internal/gateway`; daemon wires the gateway at boot (`cmd/athanor/gateway.go`). T5.4 shipped Gate G1 rule 6 (ADR-0017 §2): an AST walk in `TestGateG1NoOutboundHTTPOutsideGateway` (`internal/gate/gate_test.go`) forbids `http.Get` / `http.Post` / `http.DefaultClient` outside `internal/gateway/` or the four `cmd/athanor/cli*.go` loopback-CLI files. Constructed `*http.Client` usage is not in the rule (LLM client, Job Pod runner). The unit test `TestCheckOutboundHTTP` proves the walk actually catches the three forbidden selectors and respects the positive controls. T6 (`5928d45` + `1dc946a` + `ea5a9a1`) shipped §21.5 responsibility 6 per [ADR-0018](docs/adr/0018-reader-mode.md): `internal/gateway/reader.go` is the closed pipeline — content-type gate → `readability.FromReader` (never `FromURL`) → bluemonday `UGCPolicy` → `renderMarkdown` → prompt-injection scan — fail-closed on non-HTML, no-main-content, or injection; the dormant `network.reader_mode_default` flag is effective (`config.Network.ReaderMode()`, `startGateway` now returns a Client+Reader bundle); `reader_mode_applied` / `reader_mode_rejected` join the `network` event set (no migration); ~25 `Test*` functions in `internal/gateway/{reader,markdown}_test.go` incl. the hostile script/iframe/object/form/onerror corpus and a httptest fetch→extract e2e. Deps grow from two to four — `codeberg.org/readeck/go-readability/v2` (the **maintained** continuation; the go-shiori module ARCHITECTURE §2 named is deprecated upstream, ADR-0018 §1) and `bluemonday`; `make check` green, Gate G1 re-proven through the `x/net`/`x/sys` upgrades. T7 (`f459ced`→T7.5 close-out, ADR-0019 + [plan](docs/m4-t7-plan.md) + [demo](docs/demo-m4-t7.md)) shipped the gateway-backed tools: `fetch_url` / `search_web` join the closed envelope (per-task override only), internal API routes behind the `ToolGateway` inversion seam (`internalapi/gateway_tools.go` + the `cmd/athanor` adapter), the redirect hop loop inside `Client.Fetch` (per-hop policy re-eval + rate limit + pinned dial — closes the first-hop-only SSRF gap, `TestFetch_DeniesOffListRedirect`), `network.search_engine_url_template` (default empty → typed 501; engine host gets no allowlist exemption), raw bytes never inlined in tool responses (reader refusals → `mode=raw`; injection → 422), one halved-cap truncated re-fetch (ADR-0019 §6), and the engine research sub-step (task-declared URLs → attributed markdown in every divergence candidate, soft-fail per source; ADR-0019 §7 amended from planner-emitted sources — reality: the M1 planner output is discarded free text). T8 (adversarial security suite, `1ab9ba5` + `7939b0f` + `1fc3f22` + close-out, [demo](docs/demo-m4-t8.md)) attacked the gateway and scanner with four corpora, all green in CI: SSRF (every deny-listed CIDR at its boundaries; DNS rebinding across the M4-T7 redirect hops; integer-host obfuscation refused at both the allowlist and the CIDR guard; the ADR-0017 §4 production-wire guard regression), allowlist bypass (typosquat / userinfo / trailing-dot / scheme smuggling / CRLF / IDN with positive controls and per-denial audit assertions; deny-list override proven on a redirect hop), and content attacks (10-payload LLM-targeted injection corpus fail-closed through the Reader; a ~10 MiB gzip bomb truncated at the streamed cap; a redirect-loop bomb bounded by the hop cap) — the corpus found and fixed two real scanner weaknesses (multiline flag on the role-reassignment pattern; base64 decode-evasion via chained padded blobs, now shape+size gated). Behavioral probes (gated `ATHANOR_RUN_INTEGRATION`) pass against the real internet: default-deny refuses a real domain; an allowlisted real domain fetches + extracts end-to-end (reference run 2026-09-15). **Gate G4 closed**: adversarial suite green + research goal end-to-end on allowlisted domains (`TestDiverge_InjectsResearchIntoCandidates` + [demo-m4-t7.md](docs/demo-m4-t7.md)). |
| M5 Context Engine v1 | ⬜ Not started |
| M6 Autonomy & Feedback | ⬜ Not started |
| M7 Endurance & Release | ⬜ Not started |

Update this table as milestones progress. It is the honest heartbeat of the project.

---

## How to Use This Document

This roadmap defines **what must be true** after each milestone and **what may never be compromised**. It deliberately does not define **how** things are implemented. Implementation details emerge during development and are captured as Architecture Decision Records.

- **Tasks are hypotheses, not contracts.** If development reveals a better structure, change the approach — then update this file and write an ADR.
- **No task specifies internal APIs, schemas, or function names.** Acceptance criteria describe observable behavior only.
- **Spikes are first-class.** Anything with genuine uncertainty gets a timeboxed spike with an explicit hypothesis before it gets a build task.
- **Deviations are cheap; drift is expensive.** Changing the plan costs one ADR paragraph. Silently diverging from it costs the truth of the whole document.
- **ADRs** live in `docs/adr/NNNN-title.md` (short: context → decision → consequences). Any choice that future-you would ask "why did they do *that*?" deserves one.

### Change Policy

1. Modify tasks freely within a milestone; keep acceptance criteria verifiable.
2. Adding a milestone or reordering requires updating the Security Gates table (§3).
3. Never weaken an Invariant (§4) to make a task easier. Weaken the task instead.

---

## 1. Guiding Principles

- **Walking skeleton first.** A thin end-to-end slice (M1) beats thick horizontal layers. Integration risk is retired early, not discovered late.
- **Vertical slices over horizontal phases.** Each milestone delivers user-visible capability, not just infrastructure.
- **Risk-first.** The three biggest unknowns — rootless Podman ergonomics, Dialectical loop quality on local models, AST/structural division — get spikes before the milestones that depend on them.
- **Containment precedes capability.** No autonomous behavior is added before the guardrail that bounds it exists.
- **Spike → harden → automate.** Prove feasibility by hand, implement properly, lock in with tests.
- **TDD where acceptance criteria permit.** Most criteria are directly writable as tests. UI work is verified by scripted demos instead.
- **Small batches.** Tasks target ≤4h of effort (sizes S/M); L (4–8h) is the hard ceiling and requires explicit justification — otherwise split or spike. Commit per task with `M#-T#: <title>`.
- **YAGNI until backlog.** Good ideas go to the Parking Lot (§9), not into the current milestone.

---

## 2. Relationship to ARCHITECTURE.md

ARCHITECTURE.md is canonical for **design truth**; this roadmap is authoritative for **build order and scope**. Each task carries `Refs:` pointing at ARCHITECTURE sections. Where they appear to conflict, ARCHITECTURE wins for behavior, and the discrepancy is filed as a doc bug to fix both files.

---

## 3. Security Gates (By Construction)

Each milestone ends with a gate. The next milestone does not begin until the gate passes. This sequencing makes unsafe shortcuts structurally impossible rather than a matter of discipline.

| Gate | After | Rule that becomes enforceable |
|---|---|---|
| G0 | M0 | All state survives restart; migrations are forward-only and idempotent |
| G1 | M1 | No tool execution exists at all — the agent is provably contained to LLM + storage |
| G2 | M2 | No code executes outside a hardened Job Pod; pods have no network, no Podman socket, no host FS beyond approved mounts |
| G3 | M3 | No artifact is accepted without passing deterministic evaluation and comparison |
| G4 | M4 | No byte enters or leaves the workspace without airlock scanning; no egress except via the Gateway allowlist |
| G5 | M5 | No compaction runs above Temp 0.0; dormant chunk swaps are byte-for-byte exact |
| G6 | M6 | No external or irreversible action without HITL approval; every rejection yields a CorrectionRecord |
| G7 | M7 | System survives sleep, power loss, and 24h soak with no state loss |

**Standing rule:** the kill switch must exist and work from M1 onward, before any capability that could need stopping.

---

## 4. Invariants (The Mountains)

These are non-negotiable at every milestone. They are drawn from ARCHITECTURE.md and repeated here because roadmaps are where they go to die if not restated.

1. **Isolation rules** (§3.1): Job Pods talk only to the Core internal API with a per-job token; never to Ollama, the internet, the Podman socket, SQLite, each other, or credentials.
2. **Deterministic judgment:** evaluation and comparison always run on the `security` persona at Temperature 0.0. Generation explores; judgment does not.
3. **All compaction at Temp 0.0.** Critical context is divided losslessly, never summarized away.
4. **Persist at every transition.** Job state is serialized to SQLite after every state-machine transition, atomically. Crash recovery resumes from the last commit.
5. **HITL for external/irreversible.** Contained + reversible = proceed; external + irreversible = approval required.
6. **Git as undo.** Code changes are atomic commits on agent-created branches. Push is always HITL-gated.
7. **Context floors are floors.** Never silently reduce effective context below the task minimum; recommend model changes instead.
8. **Daydreaming output is always draft**, cannot commit, cannot modify accepted artifacts, and is disabled on battery.

---

## 5. Milestone Graph

```
M0 ──► M1 ──► M2 ──► M3 ──► M4 ──► M5 ──► M6 ──► M7
       │      │             │
       │      │             └── M4 can start once M3's evaluation phase
       │      │                 exists (gateway needed by research tasks)
       │      └── M2 spike may run in parallel with late M1
       └── docs/adr/ seeded (ADR-0001/0002 written pre-M0 during doc review)
```

Sequential by default; parallelize only where the graph allows. Sizes: **S** ≤ 2h, **M** 2–4h, **L** 4–8h. If a task overruns its size significantly, stop and split it or spike it.

---

## 6. Milestones

### M0 — Foundations

**Objective:** A runnable daemon skeleton with honest configuration, structured logging, and durable state — the substrate everything else stands on. Reuses task-000 findings (`mattn/go-sqlite3`, FTS5 build tag, sqlite-vec connection affinity).

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M0-T1 | Go module + project layout (`cmd/`, `internal/{config,store,power,...}`, `migrations/`) | S | code | `go build ./...` succeeds from clean clone | §2 |
| M0-T2 | Config loader + validation for `config.yaml` (defaults for optional, reject invalid) | M | code | Malformed, empty, missing-file, and wrong-type configs each produce specific errors; valid config loads with defaults applied | §29 |
| M0-T3 | Structured logging (slog → `state/logs/`, category-tagged JSON) | S | code | Categories per ARCHITECTURE §28 emit distinct events; rotation works | §28 |
| M0-T4 | Store layer: WAL pragmas, embedded forward-only migration runner, backup-before-migrate | M | code | Interrupted migration leaves prior version intact; rerun is a no-op | §23 |
| M0-T5a | Schema migrations — execution spine: projects, goals, tasks, jobs, actions, artifacts, events | M | code | Tables inspectable via `sqlite3`; FK constraints enforced; `updated_at` triggers present | §5, §23 |
| M0-T5b | Schema migrations — learning & control: corrections, hitl_requests, prompt_templates, personas | S | code | Same standards as M0-T5a; forward-only order preserved across both migrations | §5, §23 |
| M0-T6 | EventLog append API (append-only) | S | code | Concurrent writers serialize; no update/delete paths exist | §28 |
| M0-T7 | Health endpoint `/healthz` | S | code | Returns status, version, uptime | — |
| M0-T8 | CI bootstrap: vet, test -race, lint on every push | S | infra | CI red on injected failure; green on clean tree | — |

**Gate G0:** `go test ./...` green; kill -9 during migration leaves usable DB; config errors are actionable.

### M1 — Walking Skeleton

**Objective:** One thin, fully contained vertical slice: a goal goes in, an LLM-generated draft artifact comes out, and it survives a crash. **No tools exist yet** — the agent can only think and persist. This proves the spine (state machine, prompt assembly, personas, persistence, kill switch) before any capability adds risk.

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M1-T1 | Persona registry + Ollama client (model load with context target + temperature per persona) | M | code | Each configured persona loads its model; requests honor persona temperature/context; unreachable Ollama fails loudly; phase-pinned temperatures override persona defaults per §13.1 | §12 |
| M1-T2 | Context feasibility check honoring context floors (recommend-or-escalate path; no silent reduction) | M | code | Hardware below floor → job pauses with recommendation logged, never truncates silently | §12.3 |
| M1-T3 | Prompt assembly v1: deterministic order for the subset that exists (system, project/task context, criteria, phase instructions) with per-section token accounting | M | code | Same inputs → byte-identical prompt; token counts logged to EventLog | §11 |
| M1-T4 | Job state machine skeleton: queued → context_building → planning → diverging (single candidate) → synthesizing → comparing → completed; state persisted after every transition. Note: `evaluating`/`reflecting` edges arrive with Job Pods in M3-T1 — §8.2's mandatory `diverging → evaluating` edge applies from M3 onward (phased-introduction rationale: `docs/adr/0001-phased-state-machine.md`) | L | code | Kill -9 at any point mid-job → restart resumes from last committed state; illegal transitions rejected by tests | §8 |
| M1-T5 | Artifact store: create draft artifacts, version them, list per project | M | code | Draft artifacts persisted with type/version/status; visible via CLI listing | §9 |
| M1-T6 | Kill switch (CLI flag + endpoint) freezing all new work | S | code | Frozen state survives restart; unfreeze requires explicit command (logged) | §22 |
| M1-T7 | Minimal CLI: create project, submit goal, stream job progress | M | code | Fresh clone → running daemon → submitted goal → draft artifact on disk, in under 5 minutes of user time | — |
| M1-T8 | Quality probe (spike): run 5 sample goals across text/code/document archetypes; record qualitative findings | S | spike | Findings note in `docs/` with hypothesis → observation → implication format; informs M3 prompts. **✅ Ran 2026-08-26 — see [docs/probes/m1-quality-probe.md](docs/probes/m1-quality-probe.md) for findings; 5/5 adherence with `gemma4:12b-mlx`** | §13 |

**Gate G1:** Demo script passes end-to-end; crash-recovery test green; grep-level proof that no tool execution path exists.

### M2 — Container Spine

**Objective:** Untrusted code runs only inside ephemeral, hardened rootless Podman Job Pods that talk exclusively to the Core internal API. The Core itself still runs as a host binary during development (packaging is M7).

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M2-T1 | **Spike** (timebox 4h): rootless Podman lifecycle on dev machines — create/run/teardown a container with hardening flags, mounts, and internal-only networking. Hypothesis: flags in §21.2 behave equivalently on Linux and macOS/podman-machine | S | spike | ADR records what worked, what didn't, and the chosen invocation strategy; findings gate M2-T2 design | §21 |
| M2-T2 | Job Pod manager: full lifecycle (create, start, supervise, teardown) with hardening flags | L | code | Every pod created with read-only rootfs, cap-drop all, no-new-privileges, seccomp profile, resource limits, network=none/default | §21.2 |
| M2-T3 | Per-job tokens issued at pod creation, delivered via tmpfs mount, validated on every internal API call, revoked at teardown | M | code | Expired/wrong-job/absent tokens rejected; token never appears in env or argv | §3.2 |
| M2-T4 | Internal API: `execute_code`, `run_tests` enforcing each job's tool allowlist | M | code | Disallowed tools rejected and logged; results streamed back with exit codes/output | §25 |
| M2-T5 | Strict teardown + orphan cleanup (startup sweep kills stray pods) | M | code | After normal run, crash, and kill -9: zero surviving pods (integration test verifies) | §31.2 |
| M2-T6 | Security test suite: pod→internet denied, pod→Ollama denied, pod→podman.sock denied, pod→host FS denied, credential absence verified | M | test | All attempts fail from inside the pod; failures logged as security events | §21 |

**Gate G2:** Security suite green on both Linux and macOS; a pytest suite runs inside a pod and returns results.

### M3 — Dialectical Loop v1

**Objective:** The product's heart: multi-candidate generation, deterministic evaluation, comparison-before-acceptance — with budgets, retries, and checkpoint recovery. Context handling is deliberately naive here (assemble within floors); the MCE arrives in M5.

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M3-T1 | Full phase executor: planning, diverging (N candidates), evaluating (incl. `running_tests` sub-state), reflecting branch logic, synthesizing, comparing | L | code | State machine in §8 implemented exactly; every transition persisted; unit tests cover all legal/illegal edges; temperature resolution follows §13.1 precedence (ExplorationPath stage > phase > persona) | §8, §13 |
| M3-T2 | Evaluation phase: acceptance-criteria checks, linter runs, test runs in Job Pod; `EvaluationRecord` persistence | L | code | Records capture pass/fail, missing criteria, scores; evaluation always `security` persona Temp 0.0 (asserted in tests) | §19 |
| M3-T3 | Comparison phase: winner determination (`new\|previous\|none`) with confidence + reasons; artifact status flow enforced | M | code | Comparison rule (§19.3) implemented as a pure function over EvaluationRecords; fully unit-tested | §9, §19 |
| M3-T4 | Budgets & retries: per-phase wall-time budgets, max candidates, recovery counters, budget-exhaustion escalation | M | code | Exceeded budgets pause/escalate per policy; counters survive restart | §29 |
| M3-T5 | Git tool: local branch + atomic commit on artifact acceptance (push permanently denied until M6 HITL) | M | code | Commits land on agent-created branches; working tree clean after commit; push attempt blocked and alarmed | §14, §22 |
| M3-T6 | Crash-recovery E2E: kill Core mid-diverging / mid-evaluating / mid-testing → resume correctly; partial artifacts quarantined or draft | M | test | E2E test automates kill-and-resume at each phase; no state loss | §23.6 |
| M3-T7 | Quality probe #2: dialectical loop vs single-shot on ~10 tasks; document whether multi-candidate + comparison actually wins on local models | S | spike | Honest findings note in `docs/`; if the loop underperforms, an ADR proposes prompt/rubric changes before M6 builds on it. **Builds on M1-T8 ([docs/probes/m1-quality-probe.md](docs/probes/m1-quality-probe.md)); single-model-per-run design; 27 B `tall` persona + 9 B `ornith-1.5` for `alternative`; reuse probe helper at `spikes/m1-quality-probe/`** | §13 |

**Gate G3:** E2E demo — code-project goal → single task → 3 candidates → tests in pod → comparison → accepted commit, no user intervention; recovery test green.

### M4 — Airlock & Gateway

**Objective:** Files and network become available — but only through scanning and allowlisting. This unlocks the research workflow *safely* and makes real file work possible for code projects.

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M4-T1 | Path containment library (symlink escape, traversal, device/setuid rejection) used by every file operation | M | code | Property-based tests over adversarial path corpus all rejected; `O_NOFOLLOW` semantics on opens | §21.3 |
| M4-T2 | Ingress pipeline: `inbox/` → validate → scan → workspace or `quarantine/` | M | code | Clean files land; suspicious files quarantined with reason; originals untouched | §21.3 |
| M4-T3 | Scanner integrations: ClamAV adapter (optional at runtime), YARA rule loading, prompt-injection heuristics + `security`-persona classification | M | code | Injection corpus test set detected; scanner absence degrades to quarantine-on-uncertainty, never silent pass | §21.3–§21.4 |
| M4-T4 | Egress pipeline: validate `output/`, reject symlinks/devices/setuid/unexpected executables, scan before export | M | code | Egress of a poisoned tree fails closed; clean export works | §21.3 |
| M4-T5 | Gateway: domain allowlist (default deny), rate limiting, header sanitization, response size caps, full request logging | M | code | Non-allowlisted domains get 403 + network event; cookies/auth stripped; oversize responses truncated+flagged | §21.5 |
| M4-T6 | Reader Mode extraction (fetch → readability → sanitize → markdown) | M | code | JS-heavy pages return readable markdown, zero script content survives sanitization | §21.5 |
| M4-T7 | Tools behind the Gateway: `fetch_url`, `search_web` | M | code | Tools unreachable except via gateway; allowlist enforcement proven by test | §25 |
| M4-T8 | Adversarial security suite: traversal payloads, zip bombs, injection corpus, SSRF attempts against gateway | M | test | All attacks fail closed; events logged under `airlock`/`network` categories | §31.3 |

**Gate G4:** Adversarial suite green; a research goal completes end-to-end using only allowlisted domains.

### M5 — Context Engine (MCE) v1

**Objective:** Full-fidelity memory becomes real: lossless division, the Dormant Index, byte-exact swapping, KV-cache pressure triggers, and Temp 0.0 compaction. This is what makes long sessions possible without corruption.

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M5-T1 | **Spike** (timebox 4h): division strategies — tree-sitter bindings vs pure-Go parsing vs header-based splitting; verify byte-for-byte reassembly property on real repos; choose via ADR | M | spike | ADR with benchmarks and a round-trip property test on ≥3 languages; decision recorded before engine work | §10.1 |
| M5-T2 | Division engine: chunk store + `DormantIndex` entries (chunk ID, 1-line summary via `wide`, path/line-range metadata) | L | code | Reassembly of any divided file is byte-identical (property test); index queryable | §10.1 |
| M5-T3 | `context_swap` tool + active/dormant flush cycle | M | code | Swap loads requested chunk byte-for-byte and flushes prior active chunk intact; unit + integration tests | §25 |
| M5-T4 | KV-cache monitoring: pre-call token counting, 85%/95% triggers, context-floor violation → pause + recommend | M | code | Simulated pressure tests trigger correct action at each threshold; floor breach never silently truncates | §10.4 |
| M5-T5 | Context assembly priority queue (tiers 1–7) with strict bottom-up eviction | M | code | Overflow evicts tiers 7→6→5→4 only; tiers 1–3 never evicted (unit-tested) | §10.5 |
| M5-T6 | Temp 0.0 compaction jobs: deterministic (logs/test output) and semantic (conversations/docs), all `security` persona | M | code | Compaction determinism test: same input → same output across runs; temp asserted 0.0 | §10.3 |
| M5-T7 | `query_memory`: hybrid FTS5 + sqlite-vec retrieval over compacted memory and dormant chunks | M | code | Combined BM25+vector results returned with scores; respects sqlite-vec connection-affinity constraint from task-000 | §18.3, §25, docs/sqlite-setup |
| M5-T8 | Repository indexing pipeline (`wide` persona): embed files, update vector store, refresh Dormant Index summaries | M | code | Incremental indexing of a mid-size repo completes; new files indexed without full rescan | §17 |

**Gate G5:** Byte-for-byte swap test green; compaction determinism test green; assembly-priority unit tests green.

### M6 — Autonomy & Feedback

**Objective:** The system becomes genuinely agentic: goals decompose into DAGs, failures recover per policy, humans gate the external, and every rejection makes the next job better.

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M6-T1 | DAG decomposition (`tall` persona): phases, leaf tasks with criteria, dependency edges, validation (cycles, coverage, budget feasibility) | L | code | Generated DAGs pass validation or are rejected with reason; decomposition failure retries with simpler strategy per policy | §7 |
| M6-T2 | Dependency scheduler: execute ready tasks in order; propagate `blocked` to descendants | M | code | Diamond dependencies execute correctly; blocked propagation verified by tests | §7.2 |
| M6-T3 | Failure policies: max-retry blocking, alternative-persona re-decomposition, budget escalation to HITL | M | code | Each row of §7.2 table has an automated scenario test | §7.2 |
| M6-T4 | HITL request queue: request types, severity, expiry, approve/reject/modify/defer; pauses and resumes jobs | M | code | Paused job resumes exactly on approval; expiry denies by default; decisions logged | §20 |
| M6-T5 | `git_push` gated behind HITL approval | S | code | Push attempt creates approval request; denied push leaves remote untouched | §25 |
| M6-T6 | CorrectionRecords: captured from rejections, test failures, security scans, loops, hallucinated paths; mandatory structured rejection form fields | M | code | Every rejection yields a well-formed record; sources listed in §18.1 each produce records | §18 |
| M6-T7 | Feedback injection into prompt assembly: vector-similarity retrieval, severity/scope ordering, mute/edit/promote via API | M | code | High-severity project corrections outrank others; injection positions logged with token accounting | §11, §18.3 |
| M6-T8a | Web UI — Dashboard (jobs with phase indicators, alarms, power state, token usage) + Approvals queue (risk level, reason, approve/reject/modify) | M | code | Dashboard reflects a running job's phase within ~1s via SSE; approval decision resumes/pauses the correct job | §27 |
| M6-T8b | Web UI — Projects CRUD, artifact history with status badges and diffs, correction records management (edit/mute/promote) | M | code | CRUD round-trips persist; rejection form enforces mandatory fields (§18.4); diffs render between artifact versions | §27 |
| M6-T8c | Web UI — Watch View: live SSE token stream, collapsible phase tree, tool call log, test output, candidate artifacts, interruption queue, pause/stop/retry | L | code | Watch view shows live phase transitions for a running job; interruption note appears at next safe point, never mid-token | §20, §27 |
| M6-T9 | Feedback-loop E2E: reject artifact with structured reason → CorrectionRecord created → injected into next job → mistake avoided | M | test | Automated E2E asserts the correction's derived rule appears in the next prompt and behavior changes | §31.4 |
| M6-T10 | Strategy capture: persist StrategyProfile at job start and immutable StrategyOutcome at job end, transactionally with state transitions | M | code | Every completed job has both records; capture derives from persona plan (zero inference); migration creates `strategy_profiles` / `strategy_outcomes` / `strategy_insights` tables (forward-only); pre-capture jobs backfillable from persona_plan | §13.3 |
| M6-T11 | Strategy analysis engine: deterministic aggregation over outcomes, proposed insights at thresholds, HITL-gated activation/muting, Statistics panel | L | code | Synthetic outcome corpus yields expected insight; *proposed* insights provably never affect prompts or persona plans; promotion requires approval and is logged | §13.4 |

**Gate G6:** HITL gates every external/irreversible action (proven by attempting each type); feedback-loop E2E green; multi-task DAG demo passes unattended.

### M7 — Endurance & Release

**Objective:** The furnace burns unattended: power-aware, daydreaming, backed up, doctor-validated, packaged, and soak-tested. This milestone turns a demo into a system.

| ID | Task | Size | Type | Acceptance Criteria | Refs |
|---|---|---|---|---|---|
| M7-T1 | Power/idle integration: wire `internal/power` profiles (naming already §24-aligned and tested), AC/battery gating, sleep→flush+pause, wake→resume-from-checkpoint | M | code | Sleep/wake cycle mid-job resumes correctly; battery below threshold pauses with state flushed | §24 |
| M7-T2 | Daydreaming engine: five actions (§17.1 — memory consolidation, repo exploration, proactive documentation, feedback review, strategy mining [strategy mining depends on the records produced by M6-T10/T11]; `skill_refinement` is deferred with the skills runtime, see backlog) with constraints (priority yield, budgets, draft-only output, no battery) + `DaydreamLog` | L | code | Daydream job yields within seconds when real work arrives; outputs all draft; log persisted per session | §17 |
| M7-T3 | Alarms: categories and levels per §22.3, wired to their triggers (loop, resource, quality >50%, stuck, hallucination, budget, security, drift, self-modification) | M | code | Each alarm category has a triggered test; critical alarms freeze the system | §22 |
| M7-T4 | Backups: scheduled local backups, retention, restore drill documented and tested | S | code | Restore from backup produces working state; retention prunes correctly | §23.4 |
| M7-T5 | `athanor doctor`: full check list incl. context-feasibility and model availability with remediation suggestions | M | code | Doctor catches each seeded fault (no podman, no ollama, missing model, low RAM) with actionable output | §30.2 |
| M7-T6 | Morning Digest: async overnight summary (completed DAGs, artifacts, failures, pending approvals, daydream output, token usage) | M | code | Digest generated after simulated overnight run matches activity records | §27.2 |
| M7-T7 | Packaging: Core Pod OCI image, Job Pod base image(s), single-binary CLI install flow | L | infra | Fresh machine → install → doctor passes → first artifact, without manual steps beyond prerequisites | §32 |
| M7-T8 | Headless mode: systemd/launchd units, UI via SSH port forwarding | S | infra | Headless boot on clean Linux VM serves UI through forwarded port | §32 |
| M7-T9 | 24h soak: mixed workload + idle periods on AC power; memory/goroutine leak checks; WAL growth bounded | M | test | Zero leaks, zero unrecovered crashes, disk usage stable | — |
| M7-T10 | Docs pass: README quickstart verified against reality; ARCHITECTURE/ROADMAP discrepancies fixed | S | doc | A newcomer follows the README successfully; no doc/behavior conflicts remain | — |

**Gate G7 (release):** Soak clean · fresh-install demo < 15 minutes · security audit checklist from §31.3 signed off · all gates G0–G6 still passing.

---

## 7. Deliberately Deferred (Backlog)

Post-M7 capability. Listed so ideas have a home without derailing the current milestone.

| Item | Notes |
|---|---|
| Browser Mode | Isolated pod + proxied gateway; HITL-gated. Needs real demand before building. |
| Cloud inference mediation | Credential broker path for approved outbound calls. Design exists (§21.7); wait for need. |
| Skills runtime | Python skill objects, OCI packaging, permission declarations, scanner integration (§26). Includes the `skill_refinement` daydream action (§17.1), deferred from M7-T2. |
| Exploration Paths | User-definable prompt/temperature sequences (§13.2). Requires stable loop quality first (see M3-T7). |
| Media/data archetype depth | Toolchains, render commands, dataset validation. Text/code/document archetypes carry MVP. |
| Full Statistics / Memory Browser views | Retrieval test tool, compaction layer viewer, usage dashboards (§27). |
| Host Adapter polish | System tray states, fsnotify-driven proactive triggers. Minimal version only in M7. |
| Recurring/triggered task types | Cron and event-triggered tasks after one-time tasks prove the scheduler. |
| `artifact.Store.ListByJob` + retire raw SQL in M3-T1 tests | M3 cleanup (not post-M7). The E1 dialectical-loop test in `internal/engine/multicandidate_test.go` runs `env.db.DB().QueryContext(...)` to count proposal artifacts because no `ListByJob` method exists on `artifact.Store`; the evaluation repo has one (`internal/evaluation/repo.go`), the artifact repo does not. Add the method, route the test through `env.artifacts.*`, and remove the raw SQL. Discovered during the `sqlrowserr` fix for the M3-T1 test suite (`755e3d8`). |
| M3-T7-a: candidate-diversity measurement (Jaccard on the `divergence_candidate` event) | M3-T7 follow-up. Today `phaseDivergeN` (`internal/engine/diverge.go:55`) seeds candidates with a per-index string and audits only `{index, of, chars}`. There is no pairwise Jaccard similarity metric. If M3-T7's probe shows <0.3 Jaccard on >20% of tasks, add a per-candidate Jaccard to the `divergence_candidate` event and consider a re-roll policy below a floor. GLM review of 2026-09-01 (logged 2026-09-01 in `docs/adr/` not yet; tracking here so it doesn't get lost). Implementation is ~5 lines in `diverge.go` plus a focused test. |
| External-API `Host`-header allowlist middleware (ADR-0011) | ADR-0011 records the design; the implementation is a small `internal/server/middleware.go` (~30 lines) plus a config block. The middleware is independent of the engine and lands in a one-line commit. M3-T2.1 (the move) is a natural place because the file is `internal/server`; if M3-T2.1 is too crowded it slips to a post-M3-T2 follow-up. The M7-T5 doctor + headless packaging milestone is the deadline — browser exposure becomes a real concern there. GLM review of 2026-09-01 surfaced the DNS-rebinding class. |
| M3-T7-b: judge-confidence calibration study | M3-T7 follow-up. The §19.3 guard (`internal/engine/compare.go:101–124`) requires `confidence > min_judge_confidence` (default 0.7), but the threshold is a guess. M3-T7 should run each verdict twice (label the first as the gold answer, compare the second's confidence against it) and report a calibration table. The audit-event fields added by ADR-0013 (`content_truncated_bytes`, `content_full_bytes`) plus the new `EvaluationRecord.confidence` series are the input data. Result lands in `docs/probes/m3-quality-probe.md`. If the threshold is mis-calibrated, ADR-0013's prompt disclosure (showing the judge the truncation) should be the first lever, not changing the default. |
| M3-T7-c: verdict-stability study at Temperature 0.0 | M3-T7 follow-up. Ollama is not perfectly deterministic at T=0 — quantization, KV-cache state, and batching produce small variation. M3-T7 should run each judge call 3× and report the verdict distribution (3-same / 2-1 / 3-way). The `format: "json"` setting from ADR-0012 reduces the *parse* failure mode but does not address verdict variation. A low 3-same rate is the empirical case for either (a) raising `min_judge_confidence`, (b) requiring two `EvaluationRecord` instances to agree before accepting `winner: new`, or (c) both. No code change without the empirical data. |
| Consolidate the two verdict parsers into `parseVerdictJSON[T]` (ADR-0012 follow-up) | ADR-0012 records the design. The two near-clone brace scanners in `internal/engine/evaluate.go:269–324` and `internal/engine/compare.go:211–268` collapse into a single generic helper once the wire-level `format: "json"` change lands. The consolidation is a refactor (no behavior change) and a separate commit from the wire change. Lands in the same M3-T2 sequence as 2.1–2.3; the test corpus is the five cases in ADR-0012 §D3. After this lands, the two `parse*Verdict` functions become one-liners and the brace-scan logic lives in one place. GLM review of 2026-09-01. |
| M3 follow-up: `Repository.Transition` race guard (`b9f0bee`) | When two writers race on the same job, the loser's read observes the winner's just-committed state; `current.State == to` is now treated as `ErrConcurrentTransition` instead of `IllegalTransitionError`. Sibling test `TestConcurrentTransition_DifferentTargetsRacesCAS` exercises the SQL CAS branch on a different-target race. The pure `ValidateTransition` is unchanged. |
| M3 follow-up: `MinJudgeConfidence = 0` reachable as disabled mode (`174f3a4`) | The §19.3 deterministic guard was unreachable as a disabled mode through config because the engine overrode an explicit 0 back to 0.7. The field is now `*float64` (mirroring the existing `*bool` pattern) so the operator can distinguish "unset" (default 0.7) from "explicitly zero" (disable). `Execution.MinJudge()` resolves the field with the default applied only when nil. Tests in `internal/engine/compare_threshold_test.go` (end-to-end through `phaseCompare`) and `internal/config/config_test.go` (load layer) pin the contract. |
| M3 follow-up: dormant `Execution` flags marked deferred (`f309500`) | `require_tests_for_code`, `require_documentation_for_code`, and `compare_before_accept` are declared + defaulted to `true` so the shipped example config validates and parses, but the engine does not yet consult them. Operators who set any of these to `false` today will see no behavior change; they become effective in M6/M7. The deferral is documented inline in `config.example.yaml` and `ARCHITECTURE.md` so operators are not silently lied to. |
| M3 follow-up: `e.cfg` nil guards in engine (`834b8a8`) | Defensive `e.cfg == nil` checks added in `phaseCompare`, `phaseDivergeN`, and the engine's `call` helper, matching the existing guard in `resolveMaxReflectionLoops`. No production path constructs an Engine with `nil` cfg, so this is purely defensive consistency. |
| M3 follow-up: `DecideWinner` always returns non-empty `Reasons` (`7b787d5`) | The pure function now appends a one-line engine annotation to the `Reasons` slice on every branch (disabled, no-record, satisfied, downgrade). The `comparison` audit row the engine writes is then guaranteed to have something to read in a post-mortem, even when the LLM returned an empty `reasons` array. New test `TestDecideWinner_ReasonsAlwaysNonEmpty` pins the contract across all five branches. |
| M3 follow-up: integration-test gating documented (`ccbda5c`) | A new "Integration (behavioral) security probes" section in `DEVELOPMENT.md` explains why the five behavioral probes in `internal/jobpod/security_test.go` are gated by `ATHANOR_RUN_INTEGRATION=1` and skipped in CI. The structural argv regression test and the LLM-isolation tests run in CI; the integration probes run on a developer's machine with a real `podman` runtime. |

---

## 8. Process

### Definition of Done (per task)

- [ ] Acceptance criteria demonstrably met (test or scripted demo)
- [ ] Tests written where criteria are automatable; suite green (`go vet`, `go test -race ./...`, lint)
- [ ] No new invariant violations (§4)
- [ ] Committed as `M#-T#: <title>`; ROADMAP status updated if milestone-level
- [ ] If a design decision was made → ADR written

### Working agreements

- **Trunk-based development**, short-lived branches, merged when green.
- **Spikes are timeboxed** and must end in an ADR or a written finding — never silent abandonment.
- **Plan reviews** at milestone boundaries: update Status table, re-estimate remaining work honestly, prune tasks that reality made irrelevant.
- **Task effort targets ≤4h (S/M); L (4–8h) is the hard ceiling** (see §5 sizes). Split or spike anything that would exceed it.
- **Security suites never skip CI.** A red security test blocks merge regardless of urgency.

---

## 9. Notes for Implementers

1. **Start at M0-T1; follow the graph.** Parallelize only where §5 allows.
2. **The acceptance criteria are the test spec.** Write the test first when the criterion is automatable; write the demo script first when it isn't.
3. **When reality disagrees with the plan, believe reality.** Change the plan, write the ADR, move on. The roadmap serves the system, not vice versa.
4. **Quality probes (M1-T8, M3-T7) exist because local-model behavior is the biggest empirical unknown.** Take their findings seriously before compounding capability on top.
5. **Containment is a feature you ship, not a phase you pass.** Gates G0–G7 stay enforced after they're earned — regression in any gate re-blocks release.
