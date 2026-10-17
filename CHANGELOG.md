# Changelog

All notable changes to Athanor are recorded here. Format: one line per
`M#-T#: <title>` commit, in reverse chronological order (newest first),
with a backreference to the gate test, demo script, or probe that
proves the change. The "Prior work" section below summarizes the
commits before this changelog was initialized (F6).

New entries are appended at the top. Do not rewrite history.

## Unreleased

### Plan: the frozen benchmark — not the soak — is the controlled study — 2026-10-17

- **Corrects the instrument.** The controlled study is the **benchmark** — task
  set + procedure + **external scoring** — frozen at **x.0.0** and unchanged
  until **(x+1).0.0** ([ADR-0075](docs/adr/0075-frozen-corpus-compass.md)). The
  **soak** is one run shape (endurance); the **matrix** is the capability run
  shape. Freezing a soak says nothing about whether a change improved real
  performance.
- **MCE sensitivity is a requirement.** The task set must contain
  context-heavy tasks and multi-session memory scripts, and every run must
  report mechanism metrics (retrieval hit/attribution, context tokens, KV
  pressure/evictions, compaction fidelity, tokens, latency) alongside outcomes,
  plus a within-version A/B of config toggles.
- **No self-grading in the headline.** The headline metric uses the frozen
  external checks/tests; the engine's own verifier/judge is reported separately.
- `docs/frozen-corpus-v1.md` reworked: 40 core tasks (**40% code, ≥12
  context-heavy**), plus 3 memory scripts and 4 human-review tasks; procedure and
  scoring are part of the freeze.
- Docs corrected: ADR-0075, `docs/0.0.0-plan.md`, `AGENTS.md`, `ROADMAP.md`,
  `README.md`, `docs/soak-m7.md`.

### Plan: first-use workflow, Core Pod-only runtime, and the two-day cycle — 2026-10-17

- **0.0.0 gains a bounded first-use quality addendum (F1–F8).** The first real
  use is now part of the acceptance set: the **Core Pod as the only supported
  runtime** ([ADR-0078](docs/adr/0078-core-pod-only-runtime.md)), an onboarding
  qualification ladder, a project interview + approved plan, a
  presentation/review queue, an evidence reducer over a state dir, the
  **two-day cycle** ([ADR-0077](docs/adr/0077-two-day-cycle.md)), a frozen corpus
  v1, and a `curl` installer. Workflow + runbook:
  [docs/first-use.md](docs/first-use.md); plan: `docs/0.0.0-plan.md` §2b.
- **The soak is the per-major controlled study.** A corpus is cut at **x.0.0**
  and never edited until **(x+1).0.0**; the soak runs the **Core Pod**; a smoke
  subset on engine-surface changes and the full corpus pre-tag; not CI and not a
  release gate ([ADR-0075](docs/adr/0075-frozen-corpus-compass.md)).
- **Real use is the primary development signal.** Beta IRL use of 0.0.0
  candidates begins now; the frozen soak validates it
  ([ADR-0076](docs/adr/0076-continuous-evidence.md)).
- Docs updated: `docs/first-use.md` (new), `docs/0.0.0-plan.md`, `ROADMAP.md`,
  `AGENTS.md`, `ARCHITECTURE.md` §32, `README.md`, `docs/soak-m7.md`,
  `docs/m7-gate-g7.md`, `docs/deploy-core-pod.md`, `docs/evidence-pipeline.md`,
  `docs/frozen-corpus-v1.md`, ADR-0075/0076, and new ADR-0077/0078.

### Fix: soak robustness — duplicate file blocks and truncated verdicts — 2026-10-17

- **A model that restates a file no longer fails the candidate.**
  `toolenvelope.ParseFileMarkers` merges duplicate `=== FILE: path ===` blocks
  **last-wins** (`mergeLastWins`) instead of returning `ErrDuplicatePath`;
  stored manifests (`DecodeFiles`/`EncodeFiles`) stay strict, so a corrupt blob
  is still rejected. Found by the 0.0.0 ladder's `granite3b` run, where a
  repeated `solution.py` block hard-failed evaluation. Test:
  `TestParseFileMarkersDuplicateLastWins`.
- **A truncated verdict is salvaged, not fatal.** `extractVerdictObject` now
  repairs a JSON object cut off at `max_output_tokens` — close an open string,
  drop a dangling separator, close open containers (and drop the last top-level
  member if needed) — and reports `json (truncated→salvaged)` as a coercion, so
  a degenerate repetition loop yields a normal rejection instead of failing the
  job. An unrecoverable truncation still errors, and the error message is
  bounded. Test: `TestParseVerdictJSON_TruncatedSalvaged`.

### Tooling: one-hour mini soak + `config` preflight fix — 2026-10-16

- **`make soak-mini`** runs a 1-hour validation soak — the fast 9B model, the
  `full` arm, the bench corpus, one `kill -9` at 20m — exercising the whole
  ADR-0073 harness end to end (quality, telemetry, leaderboard, conformance
  scenarios). Overridable via `MINI_SOAK_*`; documented in `docs/soak-m7.md`.
- **`make soak-ladder`** runs the full 10-model roster time-boxed — one soak per
  model (single-slot residency) into a common tree, then one merged report — for
  a model-comparison leaderboard. `SOAK_LADDER_HOURS` sizes the window,
  `SOAK_LADDER_ONLY` narrows the task set, and kill injection is off so models
  see identical conditions.
- **`probe config -arm full` works.** The generated-config preflight used the
  headline-pair arm lookup and rejected the ablation/soak arms
  (`bestof3|reflect|full`) that `run`/`soak` accept; it now resolves the full
  arm set.

### Attempt-level escalation (ADR-0074) — 2026-10-16

- **A retry of a failed task climbs a fixed escalation ladder instead of
  re-rolling the same configuration.** The compute plan gains an attempt
  dimension: `policy.Features.Attempt` is a job's 1-based index among its task's
  jobs, and `policy.Escalate` applies the shipped ladder — attempt 2 raises the
  phase wall-time budget (1.5×, capped 2.5×), attempt 3 candidates to the hard
  ceiling, attempt 4 the reflection budget, attempt 5 swaps generation/synthesis
  to the `alternative` persona, attempt 6 nudges the generation temperature,
  attempt 7 appends a retry prompt directive — each rung only raising *toward* a
  configured bound. The engine honors the new knobs in `call`: `planBudget`
  scales each phase's budget, `planTemperature` adds the delta to generation
  phases only (judgment phases keep their determinism), and `planInstructions`
  appends the directive. The **first attempt is never escalated**, so first-pass
  corpus/soak benchmarks are unaffected, and the scheduler's existing retry loop
  drives the ladder automatically with no new seam. Tests:
  `internal/policy/escalate_test.go`, `internal/engine/attempt_test.go`; decision:
  `docs/adr/0074-attempt-escalation.md`. The learned selector over the ladder
  (ADR §4) and soak per-attempt curves are follow-ups.

### Soak: combined validation harness — telemetry, leaderboard, conformance (ADR-0073) — 2026-10-16

- **The soak reduces the event log instead of discarding it.** The engine
  already emits per-call `phase`/`persona`/`model`/token counts and its lever
  counters; the probe read only one aggregate row per job. It now reduces each
  job's events into per-phase/per-persona calls and tokens plus repairs,
  reflections, divergence re-rolls, candidate raises, KV pressure, context-floor
  violations, corrections/notes injected, and the git-as-undo outcome — carried
  in `results.json`, new `results.csv` columns, and report tables.
  `spikes/m3-t7-probe/telemetry.go`.
- **A per-model leaderboard.** `report.md` gains a grounded leaderboard — pass
  rate, tokens per passing job, wall-time p50/p95, calls/job — so a results tree
  from `soak` or `run` answers "which model should I run". The soak runbook
  documents the full-ladder benchmark (one soak per model under a common tree,
  then `report` + `measure`). `spikes/m3-t7-probe/leaderboard.go`.
- **Conformance scenarios.** The soak drives end-to-end scenarios against the
  live daemon and folds a pass/fail matrix into the report: the §22 freeze
  switch, the UI pages + SSE stream, an approved push to a **local bare remote**,
  and the §23.4 **backup → restore** cycle — all offline, each added only when it
  proves something no existing gate proves. `spikes/m3-t7-probe/scenarios.go`;
  decision: `docs/adr/0073-soak-validation-harness.md`.

### Fix: single-slot execution (ADR-0072) — 2026-10-16

- **One job, one model, one pod at a time.** Job concurrency was configurable
  (default 2) and the `autonomous` power profile raised it to **4**, while
  nothing in Athanor serialized inference — `limits.max_concurrent_llm_calls`
  was documented but never read — and single-model residency was left entirely
  to Ollama's `OLLAMA_MAX_LOADED_MODELS=1`. An idle-on-AC daemon could therefore
  run four jobs against one resident model, thrashing it.
  `PowerManager.MaxConcurrentJobs` is now **1** for every profile,
  `limits.max_concurrent_jobs` defaults to **1**, and the Ollama client enforces
  `max_concurrent_llm_calls` as a real process-wide limiter (default 1). The
  intended mode — the largest model resident at the largest context, one request
  at a time — is now a guarantee. Tests: `internal/power/manager_test.go`
  (single-slot invariant), `internal/llm/client_test.go` (call serialization);
  decision: `docs/adr/0072-single-slot-execution.md`.

### Fix: git-as-undo containment (ADR-0071) — 2026-10-16

- **Git-as-undo no longer escapes to a parent repo, moves the operator's HEAD,
  or hangs on a GPG prompt.** A 0.0.0 soak staged fixture copies (no `.git`)
  inside the developer's worktree, so the adapter's `git checkout -b athanor/<id>`
  resolved the nearest ancestor `.git` — creating and checking out agent branches
  **in the athanor repo itself** and forcing a `main` reset to recover. The
  adapter now requires `repository_path` to be a git worktree root
  (`ErrNotRepoRoot`, fail closed), commits through an **ephemeral worktree**
  (`git worktree add`/`remove`) so the checked-out branch and working tree are
  never disturbed, and disables signing and terminal prompts on every git call.
  The probe roots each staged fixture copy as its own git repository, and its
  fixture commit passes `--no-gpg-sign` / `commit.gpgsign=false` — a global
  `commit.gpgsign=true` previously blocked the run on a pinentry prompt. Tests:
  `cmd/athanor/git_client_test.go`, `spikes/m3-t7-probe/repo_source_test.go`;
  decision: `docs/adr/0071-git-as-undo-containment.md`.

### Probe: heterogeneous per-role models + DAG mode (soak) — 2026-10-16

- **A run can mirror a real deployment.** The soak accepts `-roles
  main=…,tall=…,security=…,alternative=…,wide=…` so a user's different models
  per role are exercised (generation, planning, summarizer, and the cross-family
  judge), and `-dag` enables `execution.dag_decomposition`: each goal is
  decomposed by the `tall` persona and run through the M6 dependency scheduler.
  `probeConfigYAML` resolves each role's model and declared family;
  `parseRoleModels` errors on a typo rather than silently falling back to
  `-model`; the probe now waits for every scheduled job of a decomposed goal and
  aggregates. Tests: `spikes/m3-t7-probe/rolemodels_test.go`.

### Fix: bounded MCE summarizer output (soak stall) — 2026-10-16

- **Repository indexing no longer runs away.** The `wide`-persona summarizer
  (`cmd/athanor/mce_adapter.go`) called `llm.Chat` without `MaxTokens`, so
  Ollama's `num_predict` was unset and an unstopped model produced thousands of
  tokens per chunk — 13k tokens over ~3.5 min observed during the 0.0.0 soak —
  making `POST /projects/{id}/index` take minutes per file and stalling the soak
  job loop between tasks. The request now caps output at `maxSummaryTokens`
  (256); a one-line Dormant Index summary needs far less. Test:
  `cmd/athanor/mce_adapter_test.go`.
- **The probe bounds its API calls.** `spikes/m3-t7-probe`'s `apiCall` now uses
  a client with a 20-minute timeout, so a wedged handler surfaces as an error
  row instead of hanging the soak loop forever.

### Probe: non-code fixture injection (soak data quality) — 2026-10-16

- **Document/text/data fixtures reach the model.** The probe consumed a bench
  task's `fixture:` only for `code` (staged into the Job Pod via
  `fixture_path`). A non-code fixture — the "provided module/source" a
  document/text/data goal refers to — was never shown to the model, so those
  tasks (7 of the 15-task bench set) could only fail, wasting soak cycles and
  skewing the quality readout. The probe now indexes a non-code fixture as the
  project repository (`repoSourceFor`), so it reaches the prompt through the
  MCE working set. Test: `spikes/m3-t7-probe/repo_source_test.go`.

### Qualification measurements: probe `measure` (M9-T2b) — 2026-10-16

- **The probe records `Measurement.Samples`.** A new `m3-t7-probe measure`
  subcommand reduces a results tree (generator accept rate over collected jobs)
  and an anchor run (judge Spearman over rated cases) into `measurements.json`
  in the `onboard.Measurement` shape, so `onboard.Route`'s `MinSamples` guard
  can act on how many samples back a score and `athanor models -qualification`
  consumes it directly. The M9 soak's 6/6 → 0/2 swing is why a single-pass
  pass@k must not swap a model on its own. Tests:
  `spikes/m3-t7-probe/measure_test.go`.

### Model onboarding surface: `athanor models` (M9-T2) — 2026-10-16

- **`internal/onboard` gets a production caller.** `onboard.Assess` composes the
  inventory (`Inventory`) and the routing proposal (`Route`) into one value, and
  the new `athanor models` command presents the per-role picture: whether each
  configured persona's model is installed, which installed models also fit in
  memory, and — with `-qualification <file>` (per-role `onboard.Measurement`
  JSON, e.g. from the probe's anchor/generator runs) — the advisory routing
  proposal. Applying it is a config edit; the output never mutates config
  (ADR-0049). Tests: `internal/onboard/assess_test.go`,
  `cmd/athanor/cli_models_test.go`.

### Declared deterministic checks for text/document (ADR-0070) — 2026-10-16

- **The document/text acceptance edge.** `project.Execution` gains `Checks`
  (riding `execution_json`, no migration) and `internal/verify` gains a hard
  `checksVerifier` for the `text`/`document` archetypes, mirroring the
  deterministic `checks:` the `eval/bench` corpus already scored offline
  (`required_sections`, `strict_section_order`, `exact_paragraphs`,
  `min_words`/`max_words`, `forbidden_phrases`, `must_contain`, `no_headings`,
  `no_placeholders`, `min_items`). Exposed via `POST /projects` and
  `athanor project create -checks <json>`; the probe now sends each bench
  task's checks (and, for `data`, its schema) to the project so the engine
  gates at runtime, not only after the fact. ADR:
  [0070](docs/adr/0070-declared-checks.md). Tests:
  `internal/verify/checks_test.go`, `internal/project/execution_test.go`,
  `spikes/m3-t7-probe/checks_test.go`.

### Job Pod shutdown hardening (M9 soak) — 2026-10-16

- **No orphan pods on interrupt.** Signalling the daemon mid-job could leave one
  orphan Job Pod (the F5 shutdown raced a mid-exec pod). `engine.StopPods` now
  sets a drain flag first, so a phase still in flight cannot start a new pod;
  `jobpod.Manager.Shutdown` then seals the manager (`Start` → `ErrSealed`) and
  force-removes every `athanor-job-*` container regardless of the in-memory
  map, called from `serve.go` right after `StopPods`. The M2-T5 boot sweep
  remains the backstop. Tests: `internal/jobpod/shutdown_test.go`,
  `internal/engine/pod_lifecycle_test.go`.

### Toolchain bump: Go 1.27.2 + golangci-lint v2.15.0 — 2026-10-16

- **Go toolchain → 1.27.2** (`go.mod` `toolchain` directive, every CI job, and
  the Core Pod build stage `golang:1.27.2-bookworm`).
  go1.27.1's standard library carries vulnerabilities fixed in 1.27.2 (notably in
  `html/template`, used by the UI), so the `vuln` job now scans — and builds
  with — the patched toolchain.
- **golangci-lint → v2.15.0.** The formerly pinned v2.13 reads export-data
  version 4 and cannot decode go1.27.2's version 5; v2.15.0's bundled `x/tools`
  can. The tree lints clean (0 issues) under the new linter.

### Core Pod packaging (M13) — 2026-10-16

- **Baked Core Pod config.** `deploy/core-config.yaml` now ships in the image at
  `/etc/athanor/config.yaml` (the path the image CMD loads), so a fresh
  `podman run` points Ollama at `host.containers.internal:11434` instead of the
  host-binary default `127.0.0.1`, which is unreachable inside the container.
  Previously the CMD pointed at a config that was never baked, so the daemon
  booted on defaults. Test: `internal/config/config_test.go`
  (`TestCorePodConfigParses`), `internal/deploy/deploy_test.go`.

### Prompt, logging, cron & kill-switch hardening (L5, L17–L21) — 2026-10-16

- **L5 — `TotalToken` counts joins.** Prompt assembly derives the total from the
  two assembled messages (including the `\n\n` section joins), not a sum of
  per-section estimates.
- **L20 — categories validated against the closed set.** `logging.Manager`
  rejects an unknown category even when no explicit `logging.categories` set is
  configured, so a typo cannot create a stray log file.
- **L21 — rotation does not drop an entry.** On a failed rotation rename,
  `rotatingWriter` reopens the original and still writes the entry.
- **L17 — Vixie cron day semantics.** The backup matcher accepts `7` as Sunday
  and ORs day-of-month with day-of-week when both are restricted.
- **L19 — accurate kill-switch contract.** `Freeze`/`Unfreeze` report success
  once the state is persisted (a failed audit is logged, not returned); a no-op
  `Unfreeze` on a running daemon returns nil.
- **L18 — documented freeze scope.** `Freeze` now documents that in-flight pods
  and inference run to their next phase boundary.

### Pod, gateway & API hardening (L6, L8, L9, L11–L13) — 2026-10-16

- **L11 — flag-like image rejected.** `jobpod.validateSpec` refuses an image
  beginning with `-` or containing whitespace, and `buildArgs` emits `--` before
  the image so it is always the first positional argument.
- **L12 — gateway header allowlist.** Request header sanitization is now an
  allow-list (User-Agent, Accept, Accept-Language, Accept-Encoding,
  If-None-Match, If-Modified-Since); anything else is stripped, so an unlisted
  credential header can never leave the gateway.
- **L13 — auth timing.** The internal-API middleware performs a dummy
  constant-time compare on a token-store miss so a miss does not leak job-ID
  existence via timing.
- **L8 — scanner exit-0 with output is a rejection.** `scanners.runCommand`
  requires empty stdout for a clean verdict (YARA exits 0 and prints matches).
- **L9 — configured re-fetch cap.** `gatewayToolAdapter` takes
  `network.max_response_bytes` instead of a hardcoded 10 MiB.
- **L6 — documented shell containment.** `run_tests`/`lint` comments state that
  the task-defined command is Core-resolved and the Job Pod is the boundary.

### Doctor hardening (M18, L23) — 2026-10-16

- **M18 — disk check falls back to an ancestor.** On a fresh clone the state
  dir may not exist yet; `checkDisk` now walks up to the nearest existing
  ancestor instead of warning "could not determine free disk space".
- **L23 — model matching aligned with onboarding.** `modelPresent` matches a
  bare base-name config against any installed tag (mirroring `onboard.baseName`)
  while still requiring an exact tag match for a tagged config.

### DAG, scheduler & HITL hardening (M19–M23) — 2026-10-16

- **M19 — `Ready`/`Blocked` agree on `ready`.** Both now treat a `ready` leaf as
  schedulable, removing the latent stuck-task hazard.
- **M20 — bounded phase nesting.** The DAG validator bounds the parent (phase)
  chain, not just the dependency chain, so unbounded nesting cannot pass
  `MaxDepth`.
- **M21 — duplicate dependencies rejected.** A malformed graph with a repeated
  `DependsOn` entry is refused (`KindDuplicateRef`).
- **M22 — HITL severity validated in Go.** `hitl.Repo.Create` rejects an invalid
  severity with a clear error instead of a SQLite CHECK failure.
- **M23 — stable `resume_state`.** A second `Await` on an already-awaiting job
  records the original resume state (`awaiting_from`), not `awaiting_approval`.

### Strategy analysis hardening (M14, M15, M16) — 2026-10-16

- **M14 — leave-one-cohort-out baseline.** `Mine` now compares a cohort's
  accept rate against the archetype baseline *excluding that cohort*, instead of
  a baseline a large cohort dominated.
- **M15 — real archetype on backfill.** `Backfill` derives each job's archetype
  from its project rather than stamping a synthetic `"legacy"` bucket that
  distorted per-archetype baselines.
- **M16 — unique insight per pattern.** A `UNIQUE(scope, pattern_json)` index
  (migration 0028) plus an upsert in `CreateInsight` make mining race-safe; a
  retired insight re-emerges rather than duplicating.

### Store, artifact & UI hardening (M10, M11, L14–L16, L24) — 2026-10-16

- **M11 — no orphan artifact files.** `CreateDraft`/`NewVersion` remove the
  content file on any rollback path (audit / supersede CAS / commit), so a
  retried version cannot leak a file.
- **M10 — memory-linear diff.** The UI's `lineDiff` is now Hirschberg
  (O(min(n,m)) space) with a `maxDiffLines` cap, replacing the O(n·m) matrix
  that allocated gigabytes for a large artifact pair.
- **L15 — schema-version errors propagate.** `currentVersion` returns an error
  (used by `Migrate`); `Store.Version` keeps the convenience int.
- **L16 — DSN path escaping.** `store.Open` percent-encodes `% ? # space` in the
  database path, so a path containing them no longer breaks the DSN.
- **L24 — stable backup name.** `store.Backup` captures the timestamp once.
- **L14 — documented by design.** The `events` audit columns are deliberately
  not foreign keys so the log outlives pruned entities.

### Engine hardening (M7, M8, L26–L30) — 2026-10-16

- **M7 — quorum votes decorrelated.** `runComparisonJudge` salts the derived
  judgment seed with the vote index, so `judge_count > 1` produces independent
  votes instead of near-identical ones.
- **M8 — documented precedence.** The `decidePlan` comment now states that
  `execution.policy.judge_count` is authoritative and that quorum applies to
  the comparison phase (the evaluation judge is a single advisory call).
- **L26 — nil-safe engine seams.** `call` guards a nil registry; `auditCat`
  guards a nil store.
- **L27 — corrected the `PassedTests` doc** (it is the evaluator's overall pass
  flag, not specifically "the test command exited 0").
- **L28 — re-roll discard failures audited.** The divergence re-roll no longer
  swallows `SetStatus` errors, so a below-floor candidate cannot silently stay
  `draft`.
- **L30 — removed the dead `reflecting → synthesizing` edge.** The engine always
  re-diverges after reflection; the transition table and its comment now match.
- **L29 — verified already fixed.** `strategy.Repo.CreateOutcome` is idempotent
  (`ON CONFLICT(job_id) DO NOTHING`), so `captureOutcome` cannot duplicate a row.

### Airlock hardening (M6, L7, L10) — 2026-10-16

- **L7 — base64 heuristic scans every run.** `PromptInjectionHeuristic` used
  the leftmost regex match, so a short leading run could mask a long blob later
  in the input; it now iterates all matches.
- **L10 — export path containment.** `egress.ExportPath` now routes the
  project/artifact components through `airlock/paths.Resolve`, so an unsafe id
  is an error (not a silently rewritten path); it returns `(string, error)`.
- **M6 — accurate O_NOFOLLOW doc.** Corrected the `paths.OpenNoFollow` comment
  to state that it closes the final-component TOCTOU race only, not the
  intermediate-directory race (which would need `openat2`/`RESOLVE_BENEATH`).

### Config & power hardening (M2, M12, L22, L25) — 2026-10-16

- **M2 — config bounds reject negatives.** `validateRaw` now range-checks
  `agent`, `limits`, `recovery`, `backup`, `power.daydream_*`,
  `strategy_analysis.*`, `job_pod.{pids_limit,memory_mb,cpus}`,
  `network.{rate_limit_per_minute,max_response_bytes}`; a typo like
  `max_concurrent_jobs: -3` fails loudly instead of loading silently.
- **M12 — unknown battery level fails closed.** With `require_ac_for_deep_work`
  on, an unknown battery reading (0) now pauses (conservative) instead of
  treating it as above-threshold; `require_ac_for_deep_work: false` still
  permits battery work outright.
- **L22 — Linux AC detection fails closed.** A battery present with no
  `AC`/`ADP` supply entry now reads as on-battery, not on-AC.
- **L25 — documented the battery threshold.** `battery_pause_threshold_percent:
  0` is "unset" (default 20); "never pause on battery" is expressed via
  `require_ac_for_deep_work: false`.

### MCE hardening (M1, L1–L4) — 2026-10-16

- **M1 — multi-owner chunk attribution.** Content-derived chunk ids mean
  identical bytes divided for two projects resolve to one id; the single
  `(project_id, job_id)` columns on `context_chunks` could only record one
  owner, so the second project's index was incomplete. A new
  `context_chunk_owners` table (migration 0027, backfilled) records every
  `(chunk, scope)` pair; `IndexForJob`/`IndexForProject`/`Owns`/`PruneSource`
  read and prune it.
- **L1 — 128-bit chunk ids.** `division.chunkID` widened from 16 to 32 hex
  chars (matching `compactionID`), removing the 64-bit birthday-collision risk.
- **L2 — nanosecond mtime.** The incremental-index skip now compares
  `ModTime().UnixNano()`, so a same-second, same-size edit is no longer skipped.
- **L3 — per-file model budget.** `RunOnce` hands each file the remaining
  model-call budget; `Enrich`/`embedSource` cap their calls, so one large file
  cannot exceed `MaxChunks` by an order of magnitude.
- **L4 — atomic dimension pin.** `SQLVectorIndex.Put` reads the pinned dimension
  and writes the vector in one transaction (atomic under the single-connection
  discipline), so two first-embeddings cannot leave mixed dimensions.

### D2/E1/E2/E3/E4 — housekeeping sweep — 2026-10-16

- **`athanor export` was a dead route (E1).** The CLI posted to a non-existent
  `/internal/v1/exports/<id>` path; it now posts to the daemon's real
  `POST /exports/<id>` route.
- **Morning Digest daydream-action count (E2).** `Daydream.Actions` scanned
  `COUNT(*)` into the aggregate field inside a `GROUP BY` loop, so it reported
  only the last group's count; it now sums across groups. Test:
  `internal/digest/digest_test.go` (`TestBuildAccumulatesDaydreamActions`).
- **Config-knob accuracy (D2/E3).** `verifier_min_fraction` and
  `min_anchor_agreement` are now documented as corpus-level F4 acceptance
  thresholds the engine does **not** enforce (the anchor floor needs a stored
  per-model calibration the daemon does not maintain); `self_refine`'s comment
  says default true (M9-T1), `max_diversity_rerolls`' comment says default 0,
  and `judge_mode`'s comment notes verifier is the default.
- **CI doc (E4).** DEVELOPMENT.md now describes the `integration` job as the
  required check it is, not a `continue-on-error` job.

### D1 — §19.3 guard reads a record for the final artifact — 2026-10-16

- **`phaseCompare` persists an `EvaluationRecord` for the synthesized final.**
  `DecideWinner` (the §19.3 guard) consulted only the pre-synthesis proposal
  records, so the record backing the accept/reject decision never described the
  bytes being compared — and `ListByArtifact(previousID)` returned proposal
  records rather than one for the accepted artifact the next job compares
  against. The comparison phase now builds a record for the final artifact
  (verifier pass/score, `ComparedAgainst` = previous accepted) and consults it
  in the guard; it is persisted after the cost-aware max-score so
  proposal-based scoring is unchanged. Test:
  `internal/engine/multicandidate_test.go`.

### C4 — Gate G2 blind spots closed (M4, M5) — 2026-10-16

- **Route auth is parsed, not string-counted.** The "every internal API route is
  auth-wrapped" proof counted `mux.Handle(` occurrences, which missed
  `mux.HandleFunc` and wrapped sub-muxes. It now parses `handlers.go`'s AST,
  requires every `mux.Handle` route's handler to be `authMiddleware(...)`, and
  fails on any `mux.HandleFunc`.
- **The envelope-bypass proof derives its tool set from the closed set.**
  `toolenvelope.AllTools()` is now the source of truth; the gate fails if a
  closed-set tool is neither in the handler map nor explicitly unrouted (or if
  the map carries a stale row). `toolenvelope.ToolGitOperation` is the one
  explicitly unrouted entry (Git-as-undo is Core-side, ADR-0030). Tests:
  `internal/gate/gate_g2_test.go`, `internal/toolenvelope/allowlist_test.go`
  (`TestAllToolsMatchesIsKnown`).

### C3 — Reader Mode runs a scanner set (M3) — 2026-10-16

- **`gateway.ReaderOptions.Scanners` replaces `Heuristic`.** Fetched web content
  was guarded only by the ~10-regex prompt-injection heuristic. The Reader Mode
  pipeline now runs a scanner set worst-wins: the in-tree heuristic always, plus
  YARA (with the resolved in-tree rule set) and ClamAV when their binaries are
  installed (`cmd/athanor/gateway.go:newReaderScanners`). Unlike the ingress
  registry — which treats an absent external scanner as fail-closed `Uncertain`
  — an absent scanner here is omitted, so a host without ClamAV/YARA does not
  refuse every fetched page; any non-clean verdict still fails closed. Tests:
  `internal/gateway/reader_test.go` (`TestReader_RequiresScanners`,
  `TestReader_MultiScannerWorstWins`).

### C2 — ingress reads bounded before load — 2026-10-16

- **Pre-read size cap in `airlock/ingress`.** The watcher `io.ReadAll`'d an
  inbox file before the `size` scanner saw it, so a multi-GB drop was buffered
  into memory before being rejected. It now checks `Stat().Size()` against
  `airlock.max_ingress_bytes` (the same cap the scanner enforces) and rejects an
  oversize file without reading it, leaving the original in place and auditing
  the rejection. `ingress.Options` gained the required `MaxIngressBytes` field.

### C1 — egress export-tree validation wired — 2026-10-16

- **`airlock/egress.ValidateTree` is now called.** The package doc promised an
  export-tree path-containment walk, but nothing invoked it (dead code). The
  exporter now validates the written tree and fails the export closed — removing
  it and auditing `egress_blocked` — if any entry is a symlink escape, device,
  FIFO, or setuid/setgid node. Today's single-file export makes this a guard
  against a future multi-file export.

### B1 — goal lifecycle (status advances with the graph) — 2026-10-16

- **`project.Repo.RecomputeGoalStatus` + `GoalStatus`.** A decomposed goal's
  status was written once (`decomposed`) and never advanced, so every goal read
  as in-flight forever, `digest.GoalsCompleted` was always 0, and the boot
  reconcile re-walked stalled graphs on every start. The scheduler now
  recomputes the owning goal's status after each terminal callback, reconcile,
  and task reset: `completed` when every task is completed, `decomposed` while
  anything is in flight or a blocked leaf awaits an operator Reset, and
  `failed` once a task has terminally failed with nothing in flight.
  `NonTerminalGoalIDs` became `ReconcilableGoalIDs` (`pending`/`ready`/
  `running` tasks only), so a permanently-blocked goal is no longer re-walked
  at every boot. Tests: `internal/project/dag_test.go`
  (`TestRecomputeGoalStatus`), `internal/scheduler/orchestrator_test.go`
  (`TestGoalStatusAdvancesWithGraph`).

### M9 — bounded event-log scans (delta + batch) — 2026-10-16

- **`store.EventFilter.AfterID` + `store.LatestEventID`.** `QueryEvents` had no
  `id > lastID` delta, so the dashboard/watch SSE loops re-read the entire
  append-only `events` table every 500 ms per client, and the egress exporter
  re-scanned every job event every 5 s. The filter now supports a delta cursor
  and `LatestEventID` reads the tail (a `MAX(id)` index lookup); the two SSE
  loops seed from it and fetch only new events in 500-row batches, and the
  egress poll drains the log in 500-row batches until caught up. Tests:
  `internal/store/eventlog_test.go` (`TestQueryEventsAfterIDAndLatest`,
  `TestQueryEventsAfterIDDrainsInBatches`).

### H7 — atomic job + task-status scheduling write — 2026-10-16

- **`job.Repository.CreateTx` + `project.Repo.SetTaskStatusTx`.** `advance`
  inserted a `queued` job and *then* marked the task `running`; a failure
  between the two left the task `pending` with an orphan job that the next pass
  duplicated. The two writes now commit in one transaction (the scheduler opens
  it over the store), so a failed task-status compare-and-swap rolls the job
  insert back and the pass skips the task. Test:
  `internal/scheduler/orchestrator_test.go` (`TestAdvanceRollsBackOnStatusConflict`),
  `internal/project/task_state_test.go` (`TestSetTaskStatusTx`).

### H8 — operator task-reset seam (blocked DAGs are recoverable) — 2026-10-16

- **`Scheduler.Reset` + `POST /tasks/{id}/reset` + `athanor task reset -id ID`.**
  A task that exhausted its §7.2 retry/budget bound is set `blocked` and its
  dependents are blocked with it, but nothing ever moved it back — a stalled DAG
  needed manual database edits. Reset returns the blocked task and its
  transitively blocked dependents to `pending`, and pins `retry_baseline`
  (migration 0026) so `handleFailure` counts attempts from the reset point and
  grants a fresh retry window rather than re-blocking on the very next failure.
  A task that is not `blocked` is refused (409); dependents that still depend on
  another failed/blocked task are re-blocked by the following scheduling pass.
  Tests: `internal/scheduler/orchestrator_test.go` (unblock, fresh window,
  refusal), `internal/project/task_state_test.go` (`ResetTask`).

### Hardening sweep (2026-10-16)

- **Deep review of every subsystem** (design + implementation); high/medium
  findings fixed in commits `7bf9ca7`..`9c65b3b`, the rest recorded in
  [docs/known-issues.md](docs/known-issues.md) "Open (2026-10-16 review)" and
  the design-level deferrals in [ROADMAP.md](ROADMAP.md) §7.
- **Acceptance & state machine.** The comparison verifier now runs on the full
  hash-verified artifact (was the 4 KB judge head, falsely rejecting code
  artifacts >4 KB); added the missing `paused → comparing` resume edge.
- **Reflection loop (the dialectical retry was inert).** The reflector's
  proposal is now persisted and injected into the next divergence pass, and
  `max_reflection_loops=N` yields N full retries (was N reflections but N−1
  re-divergences).
- **Safety.** A routine network denial no longer freezes the daemon (security
  alarms are now alert-level; only self-modification/drift stay critical); the
  UI page title is escaped template data (was an XSS/template-injection vector);
  MCE `Owns` no longer leaks chunks across jobs; the structural gates now also
  forbid `os.StartProcess`, `golang.org/x/sys/unix`, and raw `net.Dial` family
  outside the gateway/loopback sites.
- **Autonomy/ops.** `require_ac_for_deep_work` is honored (was dead); a stale
  terminal callback is ignored by the scheduler (was double-counting retries);
  row+audit writes are atomic across job/project/artifact; `SetTaskStatus` and
  artifact status gained CAS guards; HITL escalation requests carry a TTL and a
  boot reconcile re-applies decided-but-unapplied outcomes; feedback review
  respects muted/expired global corrections; strategy `Mine` dedups cohort
  membership.
- **I/O robustness.** Research fetches are bounded by the diverging-phase
  budget; `llm.Stream` fails loud on a truncated/aborted stream; the evaluation
  audit event uses `json.Marshal` (not `fmt.Sprintf %q`); migration 0025 adds
  indexes on `events(project_id)` and `artifacts(job_id, kind)`.

### M9-T2b-2 — routing variance guard + `>=` structure recognition

- **`onboard.Route` MinSamples guard.** A candidate measurement below
  `RoutingOptions.MinSamples` is reported but never replaces the configured
  model — the M9 soak passed 6/6 then 0/2 on the same tasks, so a single-pass
  pass@k is too noisy to drive a swap.
- **`internal/verify` structure parser.** Now recognizes the `>= N x` (and the
  `≥` glyph) min-items phrasing — the bench acceptance criteria use
  `>= 3 concrete alternatives`, which the parser previously missed.

### M9 soak — data run findings (2026-10-16)

- **M9 soak.** A data-gathering soak; findings in
  [docs/soak-m9-findings.md](docs/soak-m9-findings.md). Pass 1 (`gemma4:12b-mlx`,
  `full` arm): **all six code tasks accepted**, 60% deterministic pass rate (the
  failures are non-code structural checks). Pass 2 was partial and failed 2 code
  tasks — **variance**: the same model passed 6/6 then 0/2, so single-pass pass@k
  on H-tier code is noisy. `kill -9` recovery was clean (~8 s); M9-T4b's
  evidence-based raise fired (candidates 3 → 10); no guidance was injected.

### M9-T4b — the hard-task raise keys on evidence, not just the hint

- **M9-T4b.** Following the M9-T2b smoke (a weak planner labelled every H-tier
  code task "easy"), the candidate raise now also fires on **evidence**: a task
  class with at least `HardByEvidenceMinSamples` recorded outcomes and an accept
  rate below `HardByEvidenceMaxAcceptRate` (0.5) is treated as hard, and
  `Adaptive` will not narrow it. This closes the gap where the one hard-task
  lever was gated on a signal a weak planner gets wrong.

### M9-T2b — qualification smoke (findings + routing)

- **M9-T2b (smoke).** A pre-qualification pass; findings in
  [docs/qualification-smoke-m9.md](docs/qualification-smoke-m9.md). **Judge:**
  `granite4.2:3b` is the best local judge (Spearman 0.81, cross-family);
  `gemma4:e4b` (0.48) is below the floor. **Generator:** on the H-tier code
  suite `gemma4:12b-mlx` passed 4/5 vs `ornith-1.5:9b`'s 2/6 — a ~2.4×
  generator. **Routing (`onboard.Route`):** `main` → `gemma4:12b-mlx`,
  `security` → `granite4.2:3b` (the cross-family rule excludes the gemma judge).
  The smoke also surfaced the difficulty-hint gap (fixed in M9-T4b) and the
  `OLLAMA_MAX_LOADED_MODELS=1` operator precondition.

### M9-T8 (part) — Core Pod deploy guide

- **M9-T8 (part).** [`docs/deploy-core-pod.md`](docs/deploy-core-pod.md): build
  (`make core-image`), the multi-arch manifest, the container config
  (`ollama_url: host.containers.internal`), the `podman run` invocation with the
  Podman socket and state/workspace volumes, platform notes (the Linux socket
  path; the macOS VM-crossing volume caveat), and the verification + security
  boundary. The fresh-install demo on Linux and macOS remains a human checkpoint.

### M9-T2b-1 — routing decision layer

- **M9-T2b-1.** `internal/onboard.Route` turns per-role measurements (a
  generator's deterministic pass rate; a judge's anchor agreement) into a routing
  proposal: per role, keep the configured model or recommend the best measured
  one above `MinImprovement`, with the judge role's cross-family rule enforced
  (a same-family or unknown-family judge is never recommended). Advisory only —
  the proposal is for HITL approval and never mutates config ([ADR-0049](docs/adr/0049-model-onboarding-qualification.md)).
  The measurement runs (generator pass@k; judge anchor agreement via
  `make probe-anchor`) remain the T2b-2 work.

### Fix — per-job `system_state` scratch is cleared at terminal

- The planner difficulty hint, the reflection and self-refine counters, and the
  §10.5 suppression row are per-job scratch; a finished job now clears all of
  them, so `system_state` no longer grows one set per job. Only the suppression
  row was cleared before, and M9-T1b's default-on refinement widened the gap.

### Fix — Core Pod image details

- `deploy/core.Containerfile` uses `/usr/bin/tini` (Debian's path; the Debian
  `tini` package installs `/usr/bin/tini`, not `/usr/sbin/tini`). Added
  `.containerignore` so the Core image build context excludes VCS metadata, the
  compiled binary, runtime state, and the probe/soak result trees (SQLite
  databases).

### M9-T7 — packaging-aware doctor

- **M9-T7.** `athanor doctor` reports the deployment shape (host binary vs Core
  Pod, detected via `/run/.containerenv` / `/.dockerenv`) and warns when
  `inference.ollama_url` does not match it — a Core Pod pointed at `127.0.0.1`,
  or a host binary pointed at `host.containers.internal` (ADR-0069 §3). The
  detection is an optional `Probes` extension, so the doctor surface stays stable.

### M9-T5 — Core Pod OCI image

- **M9-T5.** `deploy/core.Containerfile` + `make core-image` build the **Core
  Pod** image ([ADR-0069](docs/adr/0069-core-pod-packaging.md)): a multi-stage
  build compiles the single `athanor` daemon (CGO + `sqlite_fts5`) into a
  `debian-slim` runtime with the Podman client and `tini`. At run time the Core
  holds the Podman socket (mounted) to spawn Job Pods and exposes the loopback
  UI on 7420; Job Pods never receive that mount. Multi-arch via a manifest
  (`linux/arm64` + `linux/amd64`, Apple Silicon first). `internal/deploy` asserts
  the artifact and the Makefile target.

### M9-T4 — harder tasks get more candidates

- **M9-T4.** A task the planner flags `DIFFICULTY: hard` now raises the
  divergence candidate count to `execution.max_hard_task_variations`
  (default 10) per §13.1 ("for hard tasks: up to 10"), instead of always running
  `divergence_candidates` (3). The divergence phase still re-clamps to the same
  ceiling, and `Adaptive` keeps the raise (it only lowers compute on easy tasks).
  Bounded re-rolls remain opt-in: the F4 probes measured diversity at 0.6–0.88,
  above the 0.30 floor, so enabling them by default would add cost for little
  benefit.

### M9-T2a — model onboarding inventory

- **M9-T2a.** `internal/onboard` maps the configured personas to the installed
  backend models and reports, per role, whether the configured model is present
  and which installed models fit as alternatives (largest-first). It is the
  inventory plumbing for [ADR-0049](docs/adr/0049-model-onboarding-qualification.md),
  not qualification: it narrows the field and never claims a model is best — that
  is M9-T2b's bounded qualification run (generator pass@k; judge agreement).

### M9-T3 — deterministic `data` verifier

- **M9-T3.** The `data` archetype gains a deterministic verification edge: a
  candidate is validated against a JSON Schema (`execution.data_schema`,
  operator-supplied) in `internal/verify`, so `data` acceptance is decided by a
  parser rather than the advisory LLM judge — the verifier-less archetype the F4
  closeout flagged (ADR-0063). A missing schema degrades to the judge; a
  violation is a hard fail. Settable via `athanor project create -data-schema`
  and `POST /projects` (`execution.data_schema`). `additionalProperties: false`
  is enforced even when no `properties` are declared.

### M9-T1 — repair-on-test-failure (capability)

- **M9-T1a.** The engine captures a failing test run's stderr (bounded tail) and
  feeds it to the self-refine repair and the reflection prompt, so a candidate is
  fixed from the *real* failure rather than a truncated reason. The judge never
  sees the output (it reads the short verifier reason only).
- **M9-T1b.** `execution.self_refine` now defaults **on** (bounded by
  `max_self_refine_loops`, default 1): a failed cycle repairs the best candidate
  and re-verifies it through the deterministic verifier (Gate G3 intact); set
  `self_refine: false` to disable.
- Milestone **M9 Productization (0.0.0)** opened: capability + Core Pod OCI
  image ([docs/0.0.0-plan.md](docs/0.0.0-plan.md)).

### 0.0.0-rc — technique-guidance loop, soak deadline, release scope (2026-10-15)

- **M8-T26b.** Synthesis no longer discards a verified candidate: it draws from
  the highest-scoring candidate that passed deterministic verification and, for
  `code`, falls back to that candidate's bytes if the rewrite fails the real
  tests (audited `synthesis_fallback_to_candidate`).
  [ADR-0068](docs/adr/0068-synthesis-keeps-passing-candidate.md). Found by the
  0.0.0 soak: code jobs failed with `winner: none` while passing candidates sat
  in the evaluation records.
- **M8-T26.** Technique mining over grounded trajectories: `strategy.Mine` now
  also groups outcomes by executed cognitive operation (a "technique") and emits
  winning/losing technique insights per archetype, anchored in a grounded
  (deterministic-verifier) verdict. Numbers are rendered from the data; no LLM.
- **M8-T27.** Advisory technique-guidance channel: `ActiveStatements` renders a
  technique insight as a prefer/avoid line grounded in its inline evidence,
  injected at the existing §11.2 position 14. Advisory only — the eligible
  operation set is never narrowed ([ADR-0067](docs/adr/0067-technique-guidance-loop.md)).
- **M8-T28.** Auto-activation of grounded winning techniques on the daydream
  Strategy Mining action and `POST /strategy/mine`, above a strict bar
  (`strategy_analysis.technique_min_accept_delta`), bounded by
  `max_active_insights`, reversible via mute (now durable against re-mining).
  Persona/candidate insights keep the HITL gate.
- **M8-T29.** Exploration blend: `policy.Explore` drops the learned persona bias
  on a deterministic per-job basis (epsilon on familiar classes, proportional on
  novel ones), gated by `novelty_reset` (keep/reset/auto) alongside the M8-T9
  reset.
- **M8-T30.** Guidance A/B in the probe: `m3-t7-probe compare -baseline <dir>`
  renders the per-tier deterministic pass-rate delta between a guidance-off and a
  guidance-on run.
- **M8-T31.** Soak deadline + instrumentation: `m3-t7-probe soak -until
  midnight|HH:MM|RFC3339` (persisted so a resume does not slide it); `/healthz`
  exports the live goroutine count and `soak.csv` samples it; the soak config
  compresses the time-triggered schedules.
- **ADR-0066** (0.0.0 scope + soak re-specification, coverage over wall-clock),
  **ADR-0067** (advisory technique-guidance loop, superseding M8-T8b-as-narrowing),
  and **[docs/m8-plan.md](docs/m8-plan.md)** (the finite 0.0.0 acceptance set).
- **fix(config).** Default `inference.ollama_url` is now `http://127.0.0.1:11434`
  — the shipped Core is a host binary, and `host.containers.internal` (the
  container→host alias) did not resolve on the host.
- **docs.** Living docs de-MVP'd (`SECURITY.md`, `ROADMAP.md`,
  `docs/sqlite-setup.md`); M7/M8 status and the soak runbook updated.

### M8-T19 — repo-backed task: exercise the MCE working set

- A bench task can declare `repo:`; the probe copies it to a throwaway dir (so
  the fixture is never mutated), sets the project's `repository_path`, and runs
  an index pass before submitting the goal. The generated probe config enables
  `context_engine.seed_active_chunk` so the top-ranked repository chunk enters
  the full-fidelity working set (tier 3) — the MCE actually giving the model its
  repository. `code-refactor-provided` is the first repo-backed task.

### M8-T17/T14 — non-code source material, injection for all archetypes, JSON-schema check

- **Source material (T17/T19).** Authored the corpus's missing non-code inputs
  and wired `fixture:` in `tasks.yaml`: `doc-api-readme` (a module),
  `doc-cited-brief` (a sectioned source), `text-level-rewrite` (a passage),
  `doc-design-adr` (a problem), `doc-changelog-categorize` (raw changes),
  `adversarial-ambiguous-spec` (an inconsistent requirement), and
  `data-json-normalize` (events + schema). Those tasks are now answerable.
- **Injection (engine).** See below — all archetypes receive their material.
- **Validation (T14).** The probe gains a minimal JSON-schema validator
  (`json_schema` is now a **gating** check) and a `must_contain` deterministic
  check; `max_words` added to the rewrite and cited-brief tasks.

### Fix — task source material is injected for all archetypes, not just code

- The fixture/source tree a task provides was only shown to the model for the
  `code` archetype (`diverge.go`), so `document`/`text`/`data` tasks that
  require provided material (a long source to summarize, a passage to rewrite,
  an event list to normalize) received only the goal and criteria. The material
  is now injected for every archetype, with an archetype-appropriate header
  (`renderFixture`). This is the benchmark feeding the MCE's intended context.

### Fix — eval-verdict truncation on long test commands + arm-agnostic probe report

- The code verifier echoed the **full** test command into its failure reason,
  which the evaluation prompt passed to the judge; a long multi-line command
  made the judge echo it and truncate its own JSON (`parsing security verdict:
  unterminated JSON object`, seen in the M8 soak). The reason now carries a
  one-line, 120-char excerpt (`internal/verify.shortCommand`).
- The probe report keyed its headline / diversity / stability tables on the
  hard-coded arm names `dialectical`/`single`, so the soak's `full` and the
  ablation arms showed zeros. The report is now **arm-agnostic**: scores per
  (model, goal, arm), diversity per (model, arm), stability per
  (model, goal, arm).

### Fix — a hanging pod tool call stalled a job forever (found by the soak)

- The per-phase wall-time budget was applied **only to LLM calls**
  (`phases.go`), never to the pod tool calls (`execute_code` / `run_tests` /
  `lint`). A candidate whose test hung (an infinite loop, or a test blocked on
  stdin) left the job in `evaluating` indefinitely — the first 4h soak wedged on
  its very first task. Pod tool calls are now bounded by the evaluating phase
  budget (`Engine.toolContext`), and a tool timeout **soft-fails the candidate**
  (`tool_deadline_exceeded`) rather than failing the job, so the loop keeps
  moving. Regression test `TestToolDeadlineDoesNotHangJob`.

### Fix — Job Pod name mismatch broke every code exec (regression from `d0d884a`)

- `Start` names the container `athanor-job-<id>` (so `Sweep` matches), but
  `Exec`, `Stop`, and `supervise` still addressed the **bare** `<id>`. Every
  `podman exec` therefore returned **125** ("no such container"), so real code
  never ran, and `Stop` never removed the pod (orphans after every code job).
  A single `podName(id)` helper now names the container everywhere. Found by
  the M8 probe smoke; regression tests in `internal/jobpod`.
- The probe's post-arm orphan check now **settles** (polls up to 15s) before
  reporting: a terminal job's `stopPod` can lag the terminal transition the
  probe observes, so an immediate check raced the teardown and reported a
  transient non-orphan.
- `code-parse-duration` in the bench corpus still pointed at a non-existent
  fixture and used `python -m pytest -q`; it now uses the pinned `solution.py`
  contract with a self-contained hidden test, like the other five code tasks.
  Its dangling `fixture:` made the engine hard-fail at evaluation (ADR-0065 §6
  fails loudly on a missing fixture).

### M8-T25 — real-world-internal workflow map

- **M8-T25.** [docs/m8-workflows.md](docs/m8-workflows.md) maps the end-to-end
  workflows 0.0.0 must perform (code single/multi-file, document/text/data,
  allowlisted research, brainstorm, daydream, digest, HITL, power) to their
  evidence, and states how much the M8-T23 soak exercises versus what stays
  demo/unit — with the external/irreversible scope explicitly excluded.

### M8-T24 — process & recovery matrix

- **M8-T24.** [docs/m8-process-matrix.md](docs/m8-process-matrix.md) consolidates
  the failure/process scenarios 0.0.0 must survive (crash per phase, migration
  idempotency, backup→restore, kill switch, HITL expiry, orphan sweep, power
  transitions, `kill -9`, sleep/wake, 24h soak) with the test or harness that
  proves each, and marks the human checkpoints. Linked from the Gate G7
  checklist.

### M8-T23 — combined endurance + quality soak harness

- **M8-T23.** The probe gains `soak`: one long-lived daemon over the corpus,
  resumable (`metrics.jsonl` is append-only), early-stoppable (SIGINT finalizes
  with results so far), and incrementally reported (a rolling report after every
  pass). `-kill-after` injects one `kill -9` + restart and records it in
  `soak-events.log`. The sleep/wake checkpoint stays human. See
  [docs/soak-m7.md](docs/soak-m7.md).
- `soak -only <names>` / `-goals N` select a subset of the goal set for smoke
  and partial runs (same semantics as `run`).
- `soak -judge-model <tag>` overrides `personas.security.model` so the deciding
  judge is a different family from the generator (F4 cross-family guard).

### M8-T21 — corpus-aware probe report

- **M8-T21.** Each job row now carries its bench tier (`E`/`M`/`H`) and the
  deterministic non-code check outcome (passed/failed kinds, advisory kinds).
  The report gains a **tier × arm deterministic pass-rate** section: non-code
  tasks use their checks; code tasks use the engine's accept. This is the read
  that answers "does the loop help, and where".

### M8-T22 — component ablation arms in the probe

- **M8-T22.** `run -ablation` selects four arms isolating the loop's
  components — `single` (N=1, no reflection), `bestof3` (N=3, no reflection),
  `reflect` (N=1, reflection on), `full` (N=3, reflection on, optional
  self-refine) — instead of the dialectical-vs-single pair. An arm now carries
  `max_reflection_loops` / `self_refine`, rendered into the generated config.

### M8-T18 — non-code check engine in the probe

- **M8-T18.** `spikes/m3-t7-probe/checks.go` turns a corpus task's `checks:`
  map into deterministic pass/fail (`required_sections`, `strict_section_order`,
  `exact_paragraphs`, `max_words`, `forbidden_phrases`, `no_headings`,
  bullet-count minimums). Semantic kinds (`json_schema`, fabrication,
  contradiction) are reported **advisory** and never gate. The corpus loader
  now carries `checks`.

### M8-T17 design — multi-file code artifacts and fixture staging (ADR-0065)

- **M8-T17 (design).** [ADR-0065](docs/adr/0065-multi-file-code-artifacts.md):
  a code candidate is a file **tree** stored as one blob; the pod stages a
  `Files` tree (sanitized relative paths) and overlays it on a task **fixture**,
  then runs the test command; git-as-undo commits the tree. Split into B1–B6;
  this makes the harder corpus's multi-file code tasks runnable.
- **B1.** `toolenvelope` gains a `File` type, `SanitizeRelPath`,
  `EncodeFiles`/`DecodeFiles`, and `IsTreeManifest`.
- **B2.** `execute_code` stages a `Files` tree into the pod scratch as a JSON
  manifest on stdin (never argv), re-validated in-pod; single-file `Code`
  remains the shorthand.
- **B3.** The engine parses `=== FILE: path ===` blocks into a candidate tree
  (`candidateFiles`); a lone `solution.py` keeps the write-and-run shorthand.
  The code prompt documents the multi-file marker convention; path traversal
  in a marker is rejected.
- **B4.** A dedicated `execution.fixture_path` (project execution config, no
  migration — the column is free-form JSON). The engine reads the fixture tree
  through a containment-safe reader (no symlinks, VCS/dep dirs skipped, size
  bounds), shows it to the model, and overlays the candidate on it for the test
  run. Distinct from `repository_path` (the evolving git-as-undo target).
- **B5.** Git-as-undo gains `CommitTree`: a multi-file candidate commits its
  files at repository-relative paths in one atomic commit; single-file and
  non-code artifacts keep the managed-namespace blob commit.
- **B6.** The six code tasks are revised to a pinned `solution.py`/package
  contract with self-contained hidden tests (bank/lru/expr verified against
  reference solutions). `code-bank-multifile` becomes the single-module
  `code-bank-atomic`; `code-refactor-provided` ships a `messy.py` fixture the
  model must refactor and extend. Single-file tasks need no fixture.

### M8-T16 — bench corpus loader in the probe

- **M8-T16.** The M3-T7 probe gains `-corpus eval/bench/tasks.yaml`: it loads
  the harder benchmark (ADR-0061) into the probe's goal shape so `run`,
  `judge`, and `report` operate on the M/H tasks instead of the locked E set.
  Fixtures, per-task check implementations, and anchor pairs are the
  remaining enabling work (M8-T17..T20).

### M8-T12 — adversarial self-critique and tree search (deferred, documented)

- **M8-T12.** Both operations are recorded as **deferred with rationale**, not
  implemented: neither can be shipped without a verification edge (a claimed
  break must be verifiable; tree search needs the harder corpus to be
  measurable). Documented in
  [docs/cognitive-operations.md §7](docs/cognitive-operations.md) under the
  ADR-0064 rule "no operation without a verification edge." The repertoire is
  not pruned; the operations are simply not invoked yet.

### M8-T11 — self-refine operation

- **M8-T11.** A new `refine` cognitive operation: when an evaluation cycle
  yields no passing candidate, the best-scoring candidate is critiqued and
  rewritten in place under a dedicated `refining` phase, then re-verified by
  the ordinary deterministic verifier — the operation's verification edge.
  Unlike reflection (prose proposal + re-diverge the whole set), self-refine
  repairs one candidate and re-evaluates only the repair. Opt-in
  (`execution.self_refine`, default false) and bounded by
  `execution.max_self_refine_loops` (default 1). The `llm` refiner call is
  attributed to `refine` in the trajectory; the re-verification to `verify`.
  [ADR-0064](docs/adr/0064-cognitive-operations.md),
  [catalog](docs/cognitive-operations.md).

### M8-T10 — exploration floor

- **M8-T10.** `strategy.OperationSamples` counts recent per-operation runs for
  a class, and `policy.ensureExploration` is the anti-collapse guard: a learned
  selection may drop a *proven* operation but never an unproven one (fewer than
  `ExplorationFloorSamples` recorded runs). The engine reads the counts through
  an optional `StrategyOperationHistory` seam. Selection still returns the
  baseline (M8-T8), so this is the guard placed before the narrowing lands.

### M8-T9 — novelty detection and the anti-rut reset

- **M8-T9.** `policy.Novelty` scores how far a task class is from history in
  [0,1] (pure; sample-count only, no embeddings). The engine's anti-rut reset
  (`execution.policy.novelty_reset: auto|reset|keep`, default `auto`) drops
  the learned persona bias on a novel class so specialisation never steers a
  genuinely new problem; the reset is audited as `novelty_reset`.
  [ADR-0064](docs/adr/0064-cognitive-operations.md).

### M8-T8 — operation-selection seam + shared vocabulary package

- **M8-T8.** The canonical cognitive-operation vocabulary and `Operation` type
  move to the leaf package `internal/cognitive`, imported by both `policy`
  (selection) and `strategy` (capture) — one source of truth, and the policy
  seam stays free of the store dependency. `policy.Plan` gains an `Operations`
  eligible-set surface: `Default` returns the baseline set (minus reflection
  when its budget is zero) and `Adaptive` narrows it on easy tasks; the
  decision is audited as `compute_planned.operations`. Execution does not yet
  vary with it (M8-T8b, once the harder corpus can justify a selection).

### M8-T7 — strategy trajectory (executed cognitive operations)

- **M8-T7.** Every terminal job now records its executed cognitive trajectory:
  migration 0024 adds `strategy_outcomes.operations_json`, holding the ordered
  operations (plan / diverge / verify / reflect / synthesize / compare /
  research / commit) with attributed calls and tokens, and grounded verdicts
  where a deterministic verifier decided. `internal/strategy` gains the
  `Operation` type and the canonical vocabulary; the engine derives the
  trajectory from the event log.
  [ADR-0064](docs/adr/0064-cognitive-operations.md),
  [catalog](docs/cognitive-operations.md).

### M8-T6 — Cognitive Operations design (ADR-0064)

- **M8-T6.** [ADR-0064](docs/adr/0064-cognitive-operations.md) and
  [docs/cognitive-operations.md](docs/cognitive-operations.md) fix the model: a
  shipped **repertoire** of cognitive operations, each with a verification
  edge, termination condition, and containment class (cognitive regulation);
  **selection** learned from grounded outcomes, calibrated by human evals,
  with the judge advisory; and a **novelty reset** toward the built-in default
  heuristics blended with exploration randomness. This is the spine for M8's
  ablation and learning substrate.

### M8-T4 — documented deliberate trust boundaries

- **M8-T4.** `SECURITY.md` records the intentional boundaries a reader might
  mistake for gaps: pod-supplied `run_tests`/`lint` commands execute inside
  the pod; the cross-site guard allows non-browser clients; the internal API
  distinguishes an envelope `403` from a gateway-policy `451`. The
  known-issues log now reads "Open: none" (O1/O2 fixed in M8-T1/T2).

### M8-T3 — scope-isolation structural gate

- **M8-T3.** `internal/gate/gate_scope_test.go` parses the shared wire types
  and fails the build when a scope-carrying `*Request` type (`Scope` /
  `ProjectID` / `JobID`) is not registered with the handler that constrains
  its scope server-side — so a future scope-trusting field cannot land
  silently. Enforces the security-review rule "no pod-supplied identifier
  may select a scope."

### M8-T2 — job context route resolves the real job (O2 + F-3)

- **M8-T2.** `GET /internal/v1/jobs/{id}` resolves a *job* to its task
  through `Repo.JobTask` and returns the job's real state; previously it
  loaded a task by a job id (a production 404) and hardcoded `"running"`
  (known-issues O2, security-review F-3). The handler harness now seeds a
  real job row (`seedProjectJob`).

### M8-T1 — gateway policy denial is distinguishable (O1)

- **M8-T1.** A gateway policy denial now returns `451` plus a new
  `toolenvelope.ErrPolicyDenied` (was a bare `403` folded into
  `ErrToolDisallowed`); the runner maps the two statuses to distinct
  sentinels and the engine audits `outcome=denied` separately
  (known-issues O1). Contract: [ADR-0019](docs/adr/0019-gateway-tools.md);
  tests: `internalapi/gateway_tools_test.go`,
  `runner/httpclient_test.go`, `engine/research_test.go`.

### F4 closeout + M8 (ADR-0063)

- **F4 closed — inconclusive.** The deterministic-verifier arm is met
  (42/42 code accepts decided without the LLM judge); the judge-trust and
  human-anchor arms are instrument-limited (saturated M3-T7 set, provisional
  anchor) and move to **M8 — Integrated Validation & Cognitive Regulation**
  (Gate G8). The repertoire of cognitive operations ships whole; selection
  is learned from grounded outcomes, and a novelty reset returns prior
  specialization toward the built-in default heuristics blended with
  exploration randomness. [ADR-0063](docs/adr/0063-f4-closeout.md).

### Verifier-first acceptance (ADR-0062)

- **`execution.policy.judge_mode` now defaults to `verifier`.** Deterministic
  verifiers decide acceptance where they apply; the LLM judge is an advisory
  tiebreaker, consulted only when no hard verifier is decisive and still bound
  by cross-family, quorum, and the §19.3 guard. `judge_mode: llm` restores the
  LLM-decided comparison. This formalizes the 10-model probe result — 42/42
  code accepts were decided deterministically while the judge was reliable but
  undiscriminating (saturated confidence, 40% verdict stability, 0.45–0.99
  scale variance). `data`/`media` remain judge-advisory (a recorded follow-up).
  [ADR-0062](docs/adr/0062-verifier-first-acceptance.md).

### G-F4 probe (2026-10-06)

- **10-model probe.** All ten models (3–35 B, five families) run as generators
  and as judges; findings in
  [docs/probes/gf4-closeout.md](docs/probes/gf4-closeout.md). Verifier arm met
  (**42/42 code accepts decided deterministically**); anchor agreement
  0.57–0.81 (8/10 ≥ 0.60); confidence still saturated; T-c only 40% 3-same;
  the dialectical loop shows no advantage on bounded code and mixed gains on
  text/document. Gate G-F4 **remains not fully closed** (human anchor rating
  pending; judge discrimination weak).
- **Probe harness fixes.** Judge-outer packet scoring (`49c3efa`), progress
  output (`3431296`), and `judge -think/-num-ctx/-max-predict` so the judge
  pass disables thinking and caps output consistently with the engine
  (`4728bf4`).
- **Agent (frontier) anchor rating.** *DeepSeek V4.1 Flash in OpenCode* rated
  the 16-case anchor ([report](eval/anchor/agent-rating-deepseek-v4.1-flash.md)):
  Spearman **0.984** vs the existing ratings (code cases 1.000), 15/16
  identical — far above the local judges' 0.57–0.81. Recorded separately from
  `ratings.csv`; a machine cross-check, not the human anchor.
- **Harder benchmark corpus** ([ADR-0061](docs/adr/0061-harder-benchmark.md),
  [spec](eval/bench/README.md)). `eval/bench/` specifies 15
  deterministic-first tasks in tiers M/H (the M3-T7 set remains the E
  baseline) across code, document, text, and
  data, plus an adversarial case and per-task anchor pairs — because the
  M3-T7 set saturated in the 10-model probe. The corpus is the specification;
  fixtures, test suites, and anchor pairs are the enabling work.

### Fixes (2026-10-06 review)

- **Containment and lifecycle hardening.** A static review of `internal/` and
  `cmd/` found and fixed: Job Pods were named by bare UUID so `Sweep` (the
  kill -9 orphan backstop) never matched them; `Stop` did not cancel the
  supervisor goroutine, which then spun on a failing `podman inspect` forever;
  `Start`'s duplicate-ID check was racy; `airlock/paths.Validate` checked
  symlink escape only at the final component, so a symlinked *directory* under
  root escaped; log rotation could permanently kill logging after a transient
  FS error; the gateway per-host rate-limiter map grew without bound; a failed
  scheduled backup re-fired within the same minute; the engine left a §10.5
  suppression row behind on the failure path; and the probe config let an
  out-of-matrix judge inherit the generator's family. Open items are recorded
  in [docs/known-issues.md](docs/known-issues.md).

### F5 — Hardening & Completion

- **F5-A (security).** Cross-site request defense ([ADR-0057](docs/adr/0057-cross-site-request-defense.md)).
  The loopback HTTP surface's Host-header allowlist (ADR-0011) closes DNS
  rebinding but not CSRF: a page the user visits can send a bodyless
  `POST /freeze` or a UI form post with the legitimate `Host`. A new
  `crossSiteMiddleware` in `internal/server` rejects state-changing requests
  (`POST/PUT/PATCH/DELETE`) unless `Sec-Fetch-Site` is `same-origin`/`none`,
  the request carries no browser signals at all (the CLI, Job Pods, curl), or
  `Origin` is one of the daemon's own origins. The origin set derives from
  the existing host allowlist, so there is no new config key; safe methods are
  untouched. Tests: `internal/server/cross_site_test.go`.

- **F5-C (config/engine).** Code acceptance gates ([ADR-0058](docs/adr/0058-verification-gates.md)).
  `require_tests_for_code` and `require_documentation_for_code` are now real,
  decisive gates (default true): a code candidate cannot pass unless its test
  command ran and passed, or it carries a recognized documentation construct
  (docstring, doc-comment block, or docs section). `compare_before_accept` is
  removed — it could only ever disable the §8.2 mandatory comparison, which
  Gate G3 forbids. Config parsing is strict, so a pre-F5 config that sets
  `compare_before_accept` now fails loudly at load. Tests:
  `internal/verify/docs_test.go`, `internal/engine/verification_gates_test.go`.

- **F5-B (MCE).** Automatic working set ([ADR-0059](docs/adr/0059-automatic-working-set.md),
  superseding ADR-0048 §T7c). The Dormant Index (§10.5 tier 6) now unions the
  job's own chunks with ranked, bounded project-repository chunks:
  `ChunkStore.IndexForProject` ranks `project_id = ? AND job_id IS NULL` rows
  by FTS5 `bm25` over the summary index (no model call), the engine passes a
  query hint from the task plus `context_engine.repository_index_limit`
  (default 20), and the adapter de-duplicates job-first. repository context
  now reaches a normal job's prompt and is one `context_swap` away. Opt-in
  tier-3 seeding (`context_engine.seed_active_chunk`, default false) is
  byte-capped by `seed_active_max_bytes`. Tests:
  `internal/mce/index_project_test.go`, `cmd/athanor/context_provider_test.go`.

- **F5-D (coverage).** Per-package coverage floor for the security-critical
  packages: `scripts/cover-check.sh` / `make cover-check` pins conservative
  statement-coverage floors on `internal/airlock/paths`, `internal/gateway`,
  `internal/toolenvelope`, `internal/internalapi`, and `internal/mce/division`
  (the whole-tree `make cover` number can hide a regression in one weak
  package). A new `coverage` CI job runs it, pinned by `internal/ci`.

- **F5-E (engine).** Shutdown semantics ([ADR-0060](docs/adr/0060-shutdown-semantics.md)).
  Graceful shutdown is **abandon-and-recover**: `SIGINT`/`SIGTERM` does not
  drain in-flight phases (state is committed per transition; `Recover` resumes
  on the next boot), but it now stops active Job Pods immediately via
  `Engine.StopPods` rather than leaving them for the M2-T5 startup sweep.
  Test: `internal/engine/pod_lifecycle_test.go`.

- **F5-F (docs).** Documentation-drift sweep: README status now reads
  M0–M6 complete / M7 code-complete / F5 hardening, the stale "M4-T8 done"
  deferred line is gone, and the M5 working-set caveat is replaced with the
  F5 resolution; ARCHITECTURE §25 gains an honest implemented/deferred Status
  column; the stale `internal/engine` package comment (MCE "arrives in M5",
  sub-steps "in synthesizing") is corrected; and the deploy unit's repository
  URL casing matches the module path.

### F4 (probe pre-work)

- **Outcome capture + pod teardown ordering.** `engine.Run` now captures a
  job's `StrategyOutcome` **before** stopping its Job Pod, so an offline
  collector never observes a terminal job with no outcome row (the M3-T7
  code-goal capture gap). The failure path now also stops its pod instead of
  leaking it to the next boot's M2-T5 sweep. Pre-work for the G-F4 closeout
  probe ([plan](docs/probes/gf4-probe-plan.md)).

### M7 — Endurance & Release

- **M7-T9/T10.** Soak harness + release checklist. `scripts/soak.sh` /
  `make soak` repeat the quality-probe workload (RSS/DB/disk sampling,
  budgets) for `SOAK_HOURS`; the runbook is `docs/soak-m7.md`. Gate G7
  close-out, the §31.3 audit table, and the demo are in
  `docs/m7-gate-g7.md` and `docs/demo-m7.md`. The 24h run, fresh-install
  timing, and audit sign-off are human checkpoints.
- **M7-T7/T8.** Packaging + headless install. `deploy/jobpod.Containerfile`
  (Job Pod base image, `make jobpod-image`), `scripts/install.sh` (binary +
  `--service`), systemd user unit / launchd agent templates, and
  `athanor start` (doctor then serve). `internal/deploy` asserts the artifacts
  and Makefile targets. [ADR-0056](docs/adr/0056-packaging.md).
- **M7-T6.** Morning Digest (§27.2). `internal/digest.Build` aggregates a
  window's completed/failed jobs (with reasons), goals/tasks, artifacts by
  status, pending HITL, active alarms, daydream output, and token usage;
  `GET /digest?hours=N` and `athanor digest`. [ADR-0055](docs/adr/0055-morning-digest.md).
- **M7-T4.** Backups (§23.4). `internal/backup` adds a snapshot wrapper, a
  retention pruner, an atomic offline restore, a dependency-free 5-field cron
  matcher, and a scheduler that audits `backup_created`. CLI:
  `athanor backup`, `athanor restore -from <file> -force`. `store.Backup`
  no longer collides on same-second snapshots. [ADR-0054](docs/adr/0054-backups.md).
- **M7-T3.** Alarms (§22.3). `internal/alarms` adds the nine-category /
  four-level model, a persistent active/resolved store (migration 0023), a
  pure `Detect` covering every category, and a `StoreLoader`-backed monitor;
  a `critical` alarm freezes through the kill switch. Surfaces: `GET /alarms`,
  `POST /alarms/{id}/resolve`, `athanor alarms`. [ADR-0053](docs/adr/0053-alarms.md).
- **M7-T2.** Daydreaming engine (§17). `internal/daydream` owns the §17.1
  closed action set + `DaydreamLog` (migration 0022); the cmd driver adds
  Proactive Documentation (`main`, draft README), Feedback Review (security,
  global correction from repeated project rules — `SourceFeedbackReview`), and
  Strategy Mining (deterministic §13.4 aggregation → proposed insights).
  `skill_refinement` stays deferred; all actions are draft-only and
  battery-gated. [ADR-0052](docs/adr/0052-daydream-engine.md).
- **M7-T1.** Power/idle integration (§24). Pure `power.Decide` policy (AC,
  battery threshold + override, active hours, idle, sleep) with a
  `Supervisor` poll loop over an injected `Observer`; platform adapters in
  `cmd/athanor/power_{darwin,linux,other}.go` (`pmset`/`ioreg`/`caffeinate`;
  `/sys/class/power_supply`/`xprintidle`/`systemd-inhibit`), Gate-G1
  allowlisted. The engine gains a `PauseGate` (battery/sleep pause, distinct
  from the kill switch) and `ResumePaused` for wake/AC-restore.
  [ADR-0051](docs/adr/0051-power-supervisor.md).
- **M7-T5.** `athanor doctor` — host/backend/config diagnostics per §30.2.
  `internal/doctor` is a pure check engine over an injected `Probes` seam;
  `cmd/athanor/doctor.go` is the only OS-touching implementation (`os/exec`,
  HTTP, `/proc`, `sysctl`), allowlisted by Gate G1. Checks: rootless Podman,
  Git, Ollama reachability, per-persona model presence (with the literal
  `ollama pull` remediation), memory, disk, state-dir writability, Job Pod
  image, §12.6 context feasibility, network policy. Fail vs. warn severity
  is documented. [ADR-0050](docs/adr/0050-doctor.md).

### F4 — adaptive judgment & verification (code complete)

- **F4-T1.** `internal/policy` — a pure compute/model-selection seam
  (`Plan{Candidates, MaxReflectionLoops, JudgeMode, JudgeCount, ModelRouting,
  DivergenceRoles}`); the engine routes every phase through it and audits
  `compute_planned`. Default reproduces pre-F4 behavior.
  [ADR-0044](docs/adr/0044-compute-policy-seam.md).
- **F4-T2.** The planner emits a `DIFFICULTY:` hint; `strategy.RecentStats`
  supplies per-archetype outcome history; `execution.policy.compute_policy`
  selects `default`/`adaptive`.
- **F4-T3.** `internal/verify` deterministic verifiers (tests/lint/structure);
  `phaseCompare` is verification-first (a decisive verifier decides; the LLM
  judge only on ties, and only cross-family); per-persona `family` config.
  Per-candidate pod tests. [ADR-0045](docs/adr/0045-verification-first-selection.md).
- **F4-T4.** Judge quorum, the reward-hacking guard (a decisive verifier
  failure overrides an LLM `new`), and `internal/judge` Spearman anchor
  calibration. [ADR-0046](docs/adr/0046-judge-protocol.md).
- **F4-T5/T6.** Heterogeneous divergence personas with a bounded Jaccard
  re-roll; cost-aware tie acceptance with cost on the comparison audit.
  [ADR-0047](docs/adr/0047-diversity-and-cost.md).
- **F4-T7.** Active-insight persona-plan bias (`policy_biased_from_insight`),
  reflection gating on security failures, and the MCE working-set scope
  decision. [ADR-0048](docs/adr/0048-reduce-and-complete.md).
- **F4-T8.** Per-phase `think` (off for judgment), `max_output_tokens`, and
  per-call bounds audited on every `llm_call`.

### F4 follow-ups (2026-10-05)

- **A1** code fences are normalized at artifact persistence (diverge +
  synthesize), not only at pod execution.
- **D** single-model residency: ARCHITECTURE §12.5 states one resident model
  (cross-family roles swap), the probe verifies via `/api/ps`, and
  `inference.keep_alive` is forwarded and audited.
- **C0** the human anchor is expanded to 16 cases (harder text/document +
  objective code labels anchored by test pass/fail).
- **B** the probe gains `-judge-mode`/`-judge-model`/`-alt-model`; the report
  shows the deterministic-accept fraction and a cross-family guard.
- **C1** `anchor -protocol pointwise5|pointwise100|pairwise` and
  `internal/judge` `KendallTau`/`PairAgreement`; measured on the 16-case
  anchor (gemma 0.61, granite 0.53, self-judge 0.77; pairwise position-biased).
- [ADR-0049](docs/adr/0049-model-onboarding-qualification.md) (proposed) —
  model onboarding & qualification; backlog.
- [Verifier-run findings](docs/probes/f4-verifier-run.md).

### M3-T7 — quality probe (in progress)

- **M3-T7.1.** Structured-judgment determinism precursor
  ([ADR-0012](docs/adr/0012-llm-format-json.md) §Implementation status).
  `format: "json"` is now sent on the `evaluating`/`comparing` phases (the
  ADR-0012 D1 wire change was previously unimplemented), and an optional
  `inference.judgment_seed: derived` pins a seed derived from the
  job/phase/candidate bytes on Temperature-0 calls. The resolved format
  and seed are recorded in every `llm_call` audit row; `llm.Request`
  gains `Seed`/`Format`; divergence is never seeded. Tests:
  `internal/llm/TestChatForwardsSeedAndFormat`,
  `internal/engine/TestJudgmentSeed`, `internal/config/TestInference_*`.
- **M3-T7.0/.2/.3/.4.** Protocol + scaffold reconciliation; pure analytics
  (Jaccard diversity, calibration bins, stability classes); the
  self-managed probe runner (generated arm configs, blind judge packets,
  read-only outcome capture, `report.md`); real-loader validation of the
  generated configs and a boot pre-flight. Findings pending the live run
  ([protocol](docs/probes/m3-t7-quality-probe.md)).
- **M3-T7.5.** Judgment JSON robustness
  ([ADR-0042](docs/adr/0042-judgment-json-robustness.md)). The smoke run
  hard-failed 3 of 4 jobs because `format:"json"` guarantees parseable but
  not *typed* JSON. `llm.Request.Format` now carries a JSON schema per
  judgment phase, the verdict parser tolerates common type drift and audits
  every coercion (`verdict_coerced`), and the evaluation prompt pins
  `score`/`confidence` to 0.0–1.0 with defensive normalization.

### Fixes

- **fix(server): apply `network.external_api_host_allowlist` at boot** —
  ADR-0011's configurable Host-header allowlist was validated by the
  config layer but never wired into the server, so the daemon was pinned
  to the built-in default list and any non-default `-addr` answered 421.
  Found during the M3-T7 pre-flight. Defaults flow through unchanged;
  configured overrides now take effect.
- **fix(engine): first-artifact acceptance no longer requires
  `better_than_previous`**
  ([ADR-0043](docs/adr/0043-first-artifact-acceptance.md)). The §19.3 guard
  downgraded a fresh project's first artifact to `none` unless the judge set
  `better_than_previous=true` — a flag with no referent when no prior artifact
  exists, which real models set inconsistently. The M3-T7 smoke failed half its
  fresh-project jobs at 0.90–0.95 quality because of it. With no previous, a
  passing, confident record now backs `"new"`.
- **fix(llm): cap output tokens per call**
  (`inference.max_output_tokens`, default 4096) — the runaway guard. A
  degenerate generation (an unbounded string in the verdict JSON) could
  otherwise stream for the full HTTP client timeout; the M3-T7 smoke saw
  evaluating calls hang for ~10 minutes. Bounding generation turns a hang into
  a fast truncation the parser rejects.
- **fix(security): `context_swap` enforces scope ownership** — the route now
  forces the scope to the authenticated job (a client-supplied scope is
  ignored) and the Core rejects a target chunk that belongs to another job or
  project (`mce.ChunkStore.Owns`), closing a cross-scope read/active-pointer
  gap in the untrusted-pod tool surface. Found in the M3-T7 deep review of the
  MCE integration.
- **fix(llm): disable thinking by default** (`inference.think`, default false) —
  a thinking-capable model (`ornith-1.5:9b`) spent the entire output-token
  budget on its reasoning phase, leaving the visible content empty
  (`no JSON object in verdict: ""`) and failing the comparing phase.
  `think: false` makes the model answer directly; `true` re-enables reasoning.
- **M3-T7.6–.8.** Probe harness completed: self-managed runner, generated arm
  configs, blind judge packets, offline third-party judging (`gemma4:12b-mlx` +
  `granite4.2:3b`), run guards + soak sampling, `-only` micro selection, and a
  reflection-off baseline (`execution.max_reflection_loops: 0`, now explicitly
  disableable). [ADR-0044](docs/adr/0044-compute-policy-seam.md) records the
  F4-T1 compute-policy design. The bounded smoke validated the harness (4/4
  jobs) and surfaced the defects above; the full run's findings seed F4
  ([plan](docs/f4-plan.md), results skeleton
  [`docs/probes/m3-t7-quality-probe-results.md`](docs/probes/m3-t7-quality-probe-results.md)).

### M6 — Autonomy & Feedback

- **M6-T11.** Strategy analysis ([ADR-0041](docs/adr/0041-strategy-analysis.md)).
  `strategy.Mine` deterministically groups captured outcomes into cohorts by
  strategy feature (persona, candidate count) and applies §13.4's thresholds
  (cohort floor, accept-rate delta, directionally-consistent confidence),
  writing **proposed** insights whose statements are rendered from the numbers
  (no LLM in the detection path). Proposed insights are provably inert:
  `ActiveStatements` returns active insights only, and the engine injects them
  at §11.2 position 14 / tier 7 (audited `strategy_notes_injected`).
  Promotion is HITL-gated through the M6-T4 queue (`strategy_analysis.auto_promote`
  default off); muting is direct. Exposed by `GET /strategy/insights`,
  `POST /strategy/mine`, `.../promote`, `.../mute`, `athanor strategy ...`, and
  a read-only Statistics panel.

- **M6-T10.** Strategy capture ([ADR-0040](docs/adr/0040-strategy-capture.md)).
  Migration 0021 adds `strategy_profiles`, `strategy_outcomes`, and
  `strategy_insights`. `internal/strategy` records a StrategyProfile at job
  start — derived with **zero inference** from the engine's fixed phase →
  persona map, resolved temperatures, and candidate count — and an immutable
  StrategyOutcome at terminal state (result from the job state plus the
  comparison winner, score from EvaluationRecords, confidence/tokens from the
  event log, wall time from the job timestamps, reflection loops from the
  typed counter). `UNIQUE(job_id)` makes capture idempotent, and a boot
  `Backfill` creates records for legacy terminal jobs. `strategy_insights`
  is created but mined in M6-T11.

- **M6-T9.** Feedback-loop E2E. `internal/engine/feedback_e2e_test.go`
  (`TestFeedbackLoopE2E`) runs a job to a document artifact, records the
  §18.4 structured rejection through `corrections.Capture`, then runs a
  second job for the same project and asserts the derived rule and the
  `RELEVANT CORRECTIONS` section appear in its prompt, with a
  `corrections_injected` audit row naming the record. It also proves the
  correction did **not** leak into the first job's prompt.

- **M6-T8.** Local web UI ([ADR-0039](docs/adr/0039-local-web-ui.md)).
  `internal/ui` (stdlib `html/template` + SSE, mounted at `/ui` on the
  loopback listener) serves Dashboard (active/terminal jobs, pending HITL
  approvals with approve/reject/defer, freeze state; the phase cell updates
  within ~1s from `/ui/events`), Projects (artifact history with an LCS diff
  against the superseded version), Corrections (the mandatory §18.4 rejection
  form plus mute/edit/promote), and Watch (`/ui/jobs/{id}/stream` multiplexes
  streamed model tokens and job events; interruption notes; pause/resume/
  cancel/retry). `llm.Client.Stream` (Ollama NDJSON) and the engine's
  nil-safe `TokenSink` and `InterruptionStore` seams back it; migration 0020
  adds `interruption_notes`. Loopback-only, no new dependency.

- **M6-T7.** Feedback injection ([ADR-0038](docs/adr/0038-feedback-injection.md)).
  `corrections.Relevant` returns the active records that apply to a project
  (project-scoped plus global), ordered severity → scope → recency and capped;
  the engine's nil-safe `CorrectionSource` seam renders them into §11.2
  position 8 / §10.5 tier 4, so a high-severity project correction outranks
  everything lower and the ladder may still evict the tier last. Every call
  appends a `feedback` `corrections_injected` audit row (ids, count, tier
  tokens, suppressed flag) and increments `applied_count` for records that
  survived into the prompt. `PATCH /corrections/{id}` gained §18.3 edit
  fields. Vector-similarity ranking is deferred to the `Relevant` seam.

- **M6-T6.** CorrectionRecords ([ADR-0037](docs/adr/0037-correction-records.md)).
  Migration 0019 completes the §18.2 shape (`artifact_id`, `scope`,
  `user_feedback`). `internal/corrections` maps all nine §18.1 sources to a
  default category/severity/derived rule and enforces the §18.4 mandatory
  rejection form (category, severity, reason, desired behavior, scope) for
  user-driven sources; `Active` returns severity-ordered records for
  injection, and `ListByProject`/`SetStatus`/`MarkApplied` back the UI. The
  engine reports a phase failure as a `runtime_error` record through a
  nil-safe `CorrectionSink`, and every capture writes a `feedback` event.
  `POST`/`GET /projects/{id}/corrections`, `PATCH /corrections/{id}`, and
  `athanor reject` / `athanor corrections` expose it. All nine sources are
  covered by tests.

- **M6-T5.** HITL-gated `git_push` ([ADR-0036](docs/adr/0036-hitl-git-push.md)).
  A push attempt — `POST /projects/{id}/push` or `athanor push -project <id>`
  — only creates a `git_push` HITL request; nothing touches the remote until
  an operator approves it. `hitl.Service` gained a per-type `Approver` seam
  invoked only on approve, and the daemon registers one that runs
  `gitClient.Push` (the Gate-G1-allowlisted Git adapter, now with an
  injectable runner) and audits `git_pushed` / `git_push_failed`. A rejected
  or expired request never invokes it, so the remote is untouched. `git_push`
  stays out of the pod tool envelope: it is a Core action, not a pod tool.

- **M6-T4.** HITL request queue ([ADR-0035](docs/adr/0035-hitl-queue.md),
  [plan](docs/m6-plan.md)). Migration 0018 adds `jobs.awaiting_from`, so the
  §8.1 `awaiting_approval` state is now reachable and resumes to exactly the
  state it left (repository-enforced); the engine parks there instead of
  erroring. `internal/hitl` provides a repo (`Create`/`Get`/`Pending`/`List`/
  `Decide`/`ExpireOverdue`) and a service (`Await`/`Decide`/`Expire`): approve
  resumes and re-enqueues the job, reject and expiry fail it, defer keeps it
  pending with a later window. `GET /hitl`, `POST /hitl/{id}/decision`, and
  `athanor hitl list|approve|reject|defer` expose the queue; the scheduler's
  M6-T3 `Escalator` seam now creates `task_escalation` requests; and a daemon
  goroutine denies overdue requests on `hitl.expiry_interval`. The first
  job-linked caller is M6-T5's HITL-gated `git_push`.

- **M6-T3.** Task failure policy ([ADR-0034](docs/adr/0034-task-failure-policy.md),
  [plan](docs/m6-plan.md)). A failed task is retried up to
  `execution.max_task_retries` (default 2, explicit 0 disables; a task's own
  `budget.max_jobs` is a tighter bound), then moves to `blocked` — which the
  M6-T2 closure propagates to its descendants — and the policy attempts
  re-decomposition before escalating. The scheduler reports the cause
  (`retries_exhausted` vs `budget_exhausted`) in a `task_retry` /
  `task_redecomposed` / `task_escalated` audit row, and a cancelled job is
  terminal (no retry). The `ReDecomposer` and `Escalator` seams are nil-safe;
  the concrete alternative-persona adapter is T3b and the real HITL writer is
  M6-T4. §7.2 rows 1–3 each have an automated scenario.

- **M6-T2.** Dependency scheduler ([ADR-0033](docs/adr/0033-dependency-scheduler.md),
  [plan](docs/m6-plan.md)). Migration 0017 rebuilds `tasks` to the canonical
  §7.3 lifecycle and adds `task_type` (M1 `in_progress`/`done` rows are
  remapped; inbound FKs, the self-reference, and the index/trigger survive).
  `internal/scheduler` provides the pure `Leaves`/`Ready`/`Blocked` graph
  functions and a mutex-serialized orchestrator (`Start`, `OnJobTerminal`,
  `Reconcile`) that executes ready leaves in dependency order and, when a task
  fails, marks its descendants `blocked`. The engine gained a nil-safe
  `SetOnJobTerminal` seam (fired on the normal terminal branch and the
  phase-error path) and the daemon reconciles non-terminal goals at boot.
  `execution.dag_decomposition` (default **false**) gates decompose-then-
  schedule on `goal submit`, so the M1 single-task path is the default.

- **M6-T1.** DAG decomposition ([ADR-0032](docs/adr/0032-dag-decomposition.md),
  [plan](docs/m6-plan.md)). A new pure `internal/dag` package parses the
  model's structured output and validates it deterministically (unique keys,
  resolved parent/dependency references, acyclicity with a topological order,
  depth/node/leaf-coverage/budget bounds); `internal/decompose` makes the
  `tall`-persona call, retries a parse or validation failure once with `main`,
  and persists the accepted graph through the new `project.Repo.CreateDAG`
  (atomic, key→ID resolved) — no migration, because the existing `tasks`
  columns already carry the graph. Exposed as `POST /projects/{id}/decompose`,
  `GET /projects/{id}/tasks`, and `athanor goal decompose`; graph bounds are
  `execution.dag_max_tasks/depth/total_jobs`. `goal submit` is unchanged until
  the M6-T2 scheduler enqueues ready tasks.

### Foundations — F3: documentation, correctness & hardening sweep

- **F3-T6.** The M3-T7-b/c quality-measurement **runbook** lands in
  [docs/probes/m3-t7-quality-probe.md](docs/probes/m3-t7-quality-probe.md):
  the manual collection recipe (event-table queries, binning, the T=0
  stability approximation and its confound), with the honest note that the
  measurement is **pending a live model run** and no findings are fabricated.
  The execution stays a ROADMAP §7 backlog item.

- **F3-T4.** Per-project execution config ([ADR-0031](docs/adr/0031-project-execution-config.md)):
  migration 0016 adds `projects.execution_json`; `project.Execution`
  (`test_command`, `build_command`, `linters`) is settable via
  `athanor project create -test-command/-build-command` and the `execution`
  object on `POST /projects`; `phaseEvaluate` runs the project's resolved
  command instead of a hard-coded `pytest -q` (code projects default to
  `pytest -q`, so existing behavior is unchanged). The execute_code language
  set stays `python` — multi-language is a separate task.

- **F3-T5.** Git-as-undo lands ([ADR-0030](docs/adr/0030-git-as-undo.md)):
  migration 0015 adds `artifacts.git_commit`; accepting an artifact commits
  it to `.athanor/artifacts/<kind>/<id>` on an `athanor/<project>` branch via
  the Core-side `cmd/athanor/git_client.go` (the second Gate-G1-allowlisted
  `os/exec` file) and records the SHA. Best-effort: no repository, a dirty
  worktree, or a git failure is audited and non-fatal. Never pushes.

- **F3-T3.** [ADR-0029](docs/adr/0029-file-io-containment-scope.md) records
  the containment scope (externally-influenced paths route through
  `airlock/paths`; internal/operator-controlled paths are exempt), and the
  ingress watcher now opens inbox files through `OpenNoFollow` instead of
  validating and re-opening by path — closing the Lstat→open TOCTOU window
  on the one path class that is genuinely attacker-named.

- **F3-T7.** CI and test rigor: the `integration` job is now **required**
  (behavioral probes gate merges, pinned by `internal/ci/ci_test.go`); new
  fuzz targets cover `airlock/paths.Resolve` and the verdict brace scanner
  (`make fuzz`); a new `internal/docs` test fails the build on any broken
  relative Markdown link (it caught one on first run, in `docs/demo-m1.md`);
  and `make cover` reports a 69.2% statement baseline.

- **F3-T9.** Repository security posture: `SECURITY.md` (private
  disclosure), `.github/dependabot.yml` (gomod + actions), and a PR
  checklist template.

- **F3-T8 (partial).** The documentation contradiction sweep: `README.md`
  now lists the full 8-tool closed set (was 7, missing `query_memory`) and
  scopes the §21.3 containment claim to externally-influenced paths;
  `ARCHITECTURE.md` §2 drops unadopted stack entries (`gopsutil`, the Web UI)
  and the unimplemented `sqlite-vec`, and corrects the `fsnotify` attribution
  (airlock ingress, not the Host Adapter); `config.example.yaml` and the
  `internal/toolenvelope` package doc drop the "two tools" era; and a stale
  `phaseCompare` doc comment is removed from `internal/engine/phases.go`.
  Intentional present-tense descriptions are unchanged.

- **F3-T2.** Error and seam hygiene: the external API's manual-export route
  maps a missing artifact/project to 404 via `errors.Is` on the store
  sentinels (the old string compare used `"artifact: not found"`, which never
  matched the real `"artifact not found"`, so it returned 500); `Engine.New`
  installs an `openFreezer` fallback so the kill-switch seam is nil-safe like
  `staticCap`; and `internal/gate/gate_test.go` loses a duplicated `violations`
  block and an unused local.

- **F3-T1.** Toolchain enforcement: `.golangci.yml` makes the linter set
  explicit and turns on the `gofmt`/`goimports` formatter checks the v2
  default set omits (three drift sites, including `internal/mce/index.go`,
  were fixed); CI gains `vuln` (`govulncheck`) and `tidy` (`go mod tidy` +
  `git diff --exit-code`) jobs with matching `make vuln` / `make tidy-check`
  targets; `go.mod` is normalized. `internal/ci/ci_test.go` pins the jobs.

### M5 — Context Engine — M5-T8: repository indexing pipeline

- **M5-T8.** The MCE gains its producer. Ten commits (`M5-T8.1`–`M5-T8.10`)
  plus the doc sweep:
  [ADR-0028](docs/adr/0028-repository-indexing.md) + [plan](docs/m5-t8-plan.md)
  lock the design — a project `repository_path` ([§6.1](ARCHITECTURE.md)),
  an `indexed_sources` manifest (migration **0014**) for incrementality, a
  bounded pipeline (walk → divide → prune → summarize → embed), a
  path+summary+content **embedding digest**, referentially-safe pruning, and
  three triggers (CLI / API / idle). `projects.repository_path` is set by
  `athanor project create -repo`. `internal/mce` gains `index_walk.go`
  (`Discover`), `manifest.go`, `index.go` (`Indexer.RunOnce`/`RunAll`), and
  `prune.go` (`PruneSource`); it still imports no `internal/llm` — the
  summarizer and embedder are the existing seams whose adapters live in
  `cmd/`. `athanor index -project` and `POST /projects/{id}/index` run it to
  completion; the §17.1 Repository Exploration daydream action runs one
  bounded pass per project under the existing power/idle/freeze gates.
  Empirical proof: an unchanged repository makes **zero** model calls on a
  second pass; an edit/delete converges; a mid-size repo is reachable via
  `query_memory` → `context_swap` (`TestIndexRepositoryEndToEnd`). **M5 is
  complete; Gate G5 is fully closed.** Demo:
  [docs/demo-m5-t8.md](docs/demo-m5-t8.md). `make check` green throughout.

### Foundations — F2: SQLite single-connection discipline

- **F2.** [ADR-0027](docs/adr/0027-sqlite-single-connection.md) turns the
  ADR-0003 single-connection contract (`SetMaxOpenConns(1)`) into an enforced
  property rather than a comment a contributor has to have read. An audit found
  the invariant already holds — nine transactions, all DB-only, and no
  `db.Conn()` anywhere — so the work is enforcement, not repair: a new
  structural gate (`internal/gate/gate_tx_test.go`) fails the build when
  `BeginTx`/`Begin(` appears in `internal/engine` or `cmd/`, or when `Conn(` is
  taken outside `internal/store`; and a runtime guard
  (`internal/store/concurrency_guard_test.go`) pins the pool cap and proves a
  concurrent mixed workload returns the one connection (a leak deadlocks the
  pool and the test hangs instead of passing quietly). The gate is
  package-shaped, so a new transaction in a storage package is a non-event
  while one in the orchestration layer is a build break. Both were proven to
  fail on a deliberate violation, then reverted.

### M5 — Context Engine — M5-T7: `query_memory` (hybrid retrieval)

- **M5-T7.** §25's `query_memory` and §10.2's Semantic Relevance axis land in
  eight commits (`a6bc52a`–`60dec1a`):
  [ADR-0026](docs/adr/0026-memory-retrieval.md) + [plan](docs/m5-t7-plan.md)
  lock the decisions — FTS5 adopted project-wide via the `sqlite_fts5` build
  tag (with a `store.CheckFTS5` boot preflight that fails loudly, naming the
  tag, before any migration runs), and the vector half as a `VectorIndex` seam
  with an **in-Go cosine** implementation over BLOB embeddings. The native
  sqlite-vec `vec0` accelerator is deferred to T7b/M7 packaging — a recorded
  deviation from the task's wording: the task-000 connection-affinity
  constraint is honored by the single seam that owns it, not by loading the
  extension. Migration **0013** adds two external-content FTS5 tables (over
  `compacted_memory.content` and `dormant_index.summary`) with
  insert/update/delete sync triggers, plus `memory_embeddings`.
  `internal/mce/query.go` is the retrieval engine: FTS5 `bm25()` and cosine
  fused with **reciprocal rank fusion** (k=60), each hit carrying per-signal
  ranks and provenance, scope isolation mandatory (an empty scope returns
  nothing rather than every project's memory). `llm.Client.Embed` speaks
  Ollama `/api/embed`. `query_memory` joins the §25 closed set (**7 → 8**),
  Core-executed behind the auth middleware and the per-job envelope, with Gate
  G2's envelope-bypass check now targeting *handler declarations* rather than
  the first file mentioning a tool name (the old search was
  alphabetical-order-dependent). The `cmd/athanor` adapter and boot wiring make
  the route live; the vector half stays inert until
  `context_engine.memory_embedding_model` is set (the `search_web`-inert
  pattern). Demo: [docs/demo-m5-t7.md](docs/demo-m5-t7.md). `make check` green
  throughout.

### CI — behavioral probes now run in CI (F1)

- **CI integration job.** `make test-integration` now covers both probe
  packages (`./internal/jobpod/...` **and** `./internal/gateway/...`), so
  the M4-T8 gateway probes are no longer orphaned, and a new
  `integration-images` target pulls the probe images (`alpine:3.20`,
  `python:3.12-alpine`) explicitly. A new `integration` job in
  `.github/workflows/ci.yml` installs/verifies podman, pulls those
  images, and runs `make test-integration` on Ubuntu on every push and
  PR. The job starts **non-blocking** (`continue-on-error`) so the first
  landing cannot break `main`; it is promoted to a required check once
  observed green on `main`. The M2 pod-hardening probes, the M2-T4b exec
  probe, and the M4 gateway probes are therefore verified continuously
  instead of only in dated manual runs (`04a809d`, `60a9a63`).

### M2 — Container Spine — M2-T4b: tool-execution dispatch

- **M2-T4b (this close-out).** Completes M2-T4: the §25 tool-execution
  loop now runs end-to-end. Six commits (`374854d`–`337aaab`):
  [ADR-0024](docs/adr/0024-engine-pod-dispatch.md) +
  [plan](docs/m2-t4b-plan.md) lock the design (one pod per job +
  `podman exec`, code on stdin, a `PodExecutor` seam behind the existing
  hop, the `job_pod.image` posture, a Gate G2 exec-argv arm);
  `internal/jobpod` gains `Exec` + `args_exec.go` + a stdin-capable
  `Client.RunStdin` (a non-zero command exit is a result, not an error);
  the internal API's `execute_code` / `run_tests` / `lint` 501 stubs
  become a real `PodExecutor` dispatch with typed error mapping (404
  no-pod / 409 not-running / 503 not-configured); the `cmd/athanor`
  adapters wire the executor and the engine lifecycle over
  `jobpod.Manager`; and `internal/engine` ensures the pod before the
  `code`-archetype sub-steps and stops it at terminal state (§23.6
  recovery included). Corrected in passing: the `job_pod.image` "fails
  fast at boot" claim was false and would have broken the fresh-clone
  boot, so the daemon warns at boot and refuses dispatch (503) instead
  (ADR-0024 §6). `make check` green. A real-pod end-to-end probe
  (`internal/jobpod/exec_integration_test.go`,
  `TestExec_Integration_RealPod`) proves the exec path against live
  podman — verified 2026-09-30 (macOS / podman 6.0.2 / libkrun) and green
  alongside the five M2 security probes via `make test-integration`.

### M5 — Context Engine (in progress)

- **M5-T6 (this close-out).** Temp 0.0 compaction lands in seven commits
  (`893293e`–`594ae60`, this one): [ADR-0025](docs/adr/0025-compaction.md) +
  [plan](docs/m5-t6-plan.md) lock content-addressed determinism, the §10.3
  treatment matrix, the dedicated `CompactStore`, and the minimal daydream
  driver; `internal/prompt/compact.go` adds the two versioned §11.1
  Internal-Runtime prompts (`CompactDeterministicSystemV1`,
  `CompactSemanticSystemV1`) and the pure `CompactMessages` builders;
  `internal/mce/compact.go` adds `Profile`/`TreatmentFor` (Division is the
  safe default, `code` never compacts), the `Compactor` seam, the §10.3
  `security`/0.0 constants, and `CompactStore.CompactMemory` — the
  content-address cache (`UNIQUE (kind, input_hash)`) makes "same input →
  same output across runs" true even though Ollama at 0.0 is not
  bit-reproducible; migration 0012 adds `compacted_memory`
  (`CHECK (temperature = 0.0)`); `internal/mce/consolidate.go` adds the
  `Consolidator`; `cmd/athanor/compaction_adapter.go` is the security-persona
  adapter (refuses oversize input rather than letting Ollama truncate); and
  `cmd/athanor/daydream.go` is the minimal §17.1 memory-consolidation driver
  (gated by config, power profile, kill switch, and idle; deterministic +
  semantic sources over the event log and accepted docs). The headline test is
  `TestCompactionDeterminism` (an unstable fake compactor proves the cache is
  what makes the output stable). **Gate G5 is closed** — all three arms green
  (byte-exact swap, assembly priority, compaction determinism). The full
  Daydream Engine and the OS AC/battery watcher remain M7-T1/T2; reading
  compacted memos into tier 5 is M6.

- **M5-T5 (this close-out).** The §10.5 context assembly priority queue and
  its eviction ladder land in seven commits (`0e85896`–`b947496`):
  `0e85896` [ADR-0023](docs/adr/0023-context-assembly-priority.md) + the
  [plan](docs/m5-t5-plan.md) lock the decisions (pure tier algebra in
  `internal/prompt`, explicit tier↔§11.2 mapping, ceiling, persisted
  suppression, the read path, the honest tool manifest, and the T5/T8
  content boundary); `ebf08e9` the tier model and ladder — strictly
  bottom-up 7→6→5→4, with tiers 1–3 structurally unreachable from
  `ladderOrder` and a 12-row simulated-pressure table as the acceptance
  criterion; `60540e4` rendering + two-phase `Assemble` — every populated
  §11.2 section is priced per tier, the ladder decides presence, and the
  emitted order stays §11.2 (the Dormant Index carries the §10.4
  `context_swap` invitation, and §11.2 §2 became envelope-aware so the
  prompt never claims "no tools" while a task's `allowed_tools` grant
  some); `75661e0` `ChunkStore.IndexForJob` + migration 0011
  (`idx_context_chunks_job`, index-only); `098d3b2` the engine wiring —
  `ContextProvider` (active chunk → §11.2 §7, Dormant Index → §11.2 §10),
  ceiling = `kv_cache_critical_threshold × persona num_ctx`, suppression
  persisted in `system_state` (`context:evicted:<job-id>`, cleared at
  terminal state, so a §23.6 restart does not resurrect evicted tiers),
  the ladder as the §10.4 `Evictor` seam (signature evolved to take the
  tier weights — ADR-0022 §5 anticipated it), candidate artifacts moved out
  of `extraInstructions` into §11.2 §12 at tier 3 (ADR-0013's 4 KB
  comparison bound now applied engine-side, before assembly), and a
  `kv_cache_assembled` audit row per call; `b947496` the `cmd/athanor`
  adapter + boot wiring, with the MCE runtime now built before the engine.
  **Gate G5's assembly-priority arm is green** (Gate G5 still needs T6's
  compaction determinism). One behavioural consequence is recorded rather
  than hidden: because the ceiling *is* the critical threshold, a normal
  job relieves pressure at assembly time, so §10.4's `critical` arm narrows
  to "the pinned tiers alone no longer fit" — T4's force-evict-and-proceed
  branch remains the seam's contract (unit-tested) but is unreachable from
  a normal job, and T4's end-to-end test was replaced by one asserting the
  stronger property (a pressured job completes, records the eviction, keeps
  its suppression, and never goes critical). No new dependency, no route
  change; migration 0011 is index-only.

- **M5-T4 (this close-out).** The KV-cache monitor lands in four commits
  (`e7b838c`–`79b494e`): `e7b838c` [ADR-0022](docs/adr/0022-kv-cache-monitoring.md)
  + [plan](docs/m5-t4-plan.md) lock the decisions; `f28fadf` the pure
  `mce.Assess(activeTokens, maxContext, config.ContextEngine)` — strictly-`>`
  triggers at the configured 85%/95% thresholds, `>=` floor breach (a full
  window has no room for even one completion token), fail-closed on a
  nonsensical window — with a 15-row simulated-pressure table pinning the
  exact boundaries (0.85 → none, 0.95 → warn, 1.0 → floor_breach);
  `79b494e` the engine gate in `call()`: **every** call appends a
  `kv_cache_pressure` row (category `inference`, §28.1), `warn` audits only
  (the `context_swap` suggestion and the move of oldest non-pinned chunks to
  Dormant are M5-T5's tier work), `critical` force-evicts through the new
  nilable `Evictor` seam and pauses when nothing is evictable, and a floor
  breach pauses with `context_floor_violation` + `trigger: kv_cache_monitor`
  **before the request is built** — Ollama silently truncates at `num_ctx`,
  so "Athanor never silently truncates context" is now enforced per call
  rather than assumed. `max_context` is the persona's `ContextTarget` (the
  `num_ctx` actually loaded, §12.3) so the decision is deterministic and
  testable without a live Ollama; Ollama's `prompt_eval_count` is recorded
  alongside for calibration. Five engine tests derive their windows from a
  measured baseline prompt (the argmax persona's window is shrunk exactly),
  so the pause/warn/critical/evict/eviction-error paths are deterministic
  rather than guessed. The `Evictor` seam is the M5-T5 integration point —
  production wires nil today, the ADR-0021 §10 "trigger is T4" deferral is
  satisfied on the trigger side, and the prompt half remains T5. No new
  dependency, migration, or route: Gate G1 re-proven, Gate G5 untouched.

- **M5-T3 (this close-out).** The active/dormant swap lands in four commits:
  `409ff34` migration 0010 + `internal/mce/active.go` (`Swap` loads the target
  chunk byte-exact, returns the chunk it replaced **intact**, and persists the
  active pointer so a restart resumes the same working set — §23.6);
  `a51cc0e` `context_swap` joins the §25 closed tool set (6 → 7) with
  `POST /internal/v1/jobs/{id}/context_swap`, envelope-gated and dispatched
  through a `ContextSwapper` interface, with Gate G2 route + envelope-bypass
  coverage; `8bbdb0e` the `cmd/athanor` adapter over the chunk store with
  error translation (`mce.ErrNotFound` → 404, swapping disabled → 501) and the
  boot wiring that makes the route live. **Gate G5's byte-exact-swap arm is
  closed.** The engine does not call the tool automatically yet — the trigger
  is M5-T4 (KV-cache 85%/95%) and the prompt integration is M5-T5, the same
  deferral shape `git_operation` used in M3-T5.

- **M5-T2 (this close-out).** The MCE's lossless half lands in seven
  commits: `863bbf0` tree-sitter dependencies + `internal/mce/division`
  (hybrid divider — tree-sitter for Go/Python/JavaScript, structural
  headers for markdown/plain text, fixed-line fallback); `ddf2c8c` the
  P1–P5 property suite (198 files / 5 languages, Go boundary agreement
  **1.0000/0.8872**, reproducing the M5-T1 spike); `8805a2a` migration 0009
  + the `internal/mce` chunk store (deterministic content-derived chunk
  IDs, byte-exact SQLite reassembly, Dormant Index); `efbe3a3` ingestion
  through §21.3 path containment + the three `context_engine.division_*`
  bounds and the `enable_lossless_swapping` gate; `5b6b558` the
  `Summarizer` seam + `wide` adapter at temperature 0.0; `80cb970` the
  daemon boot wiring (a database without migration 0009 fails at boot).
  Decisions: [ADR-0021](docs/adr/0021-mce-chunk-store.md);
  [ADR-0020](docs/adr/0020-division-strategy.md) becomes **Accepted** —
  the tree-sitter dependency is adopted. `internal/mce` does not import
  `internal/llm`, so a future `context_swap` route keeps Gate G2 rule 1's
  intent intact. Gate G5's byte-exact half is proven (division round-trip
  and SQLite round-trip); the swap half is M5-T3.

- **M5-T1 (division-strategy spike, `905de23`).** Three strategies —
  tree-sitter, pure-Go scanners, header-regex — raced over a 196-file /
  1.14 MB corpus. All three satisfied the byte-partition property
  (P1–P5), so the differentiator was boundary quality and dependency
  cost. Findings: [docs/probes/m5-t1-division.md](docs/probes/m5-t1-division.md);
  decision: [ADR-0020](docs/adr/0020-division-strategy.md).

### Security

- **fix(deps): golang.org/x/text v0.37.0 → v0.39.0** — patches
  [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) (infinite loop on
  invalid input). Reachable from the Reader Mode path: `reader.extractHTML`
  calls `bluemonday.Policy.Sanitize`, which eventually calls
  `norm.Form.Transform`. Still an indirect dependency; `make vuln` is clean.

- **fix(jobpod): noexec on the Job Pod /tmp tmpfs** —
  `args_common.go` mounted `/tmp` `rw,nosuid,nodev`, but §21.2 documented
  `noexec`; the two had drifted. `/tmp` now carries
  `rw,noexec,nosuid,nodev,size=8m`, so a pod cannot stage and execute a
  dropped binary from its scratch space.

- **fix(jobpod): valid Podman seccomp profile path** —
  `args_linux.go` passed `--security-opt seccomp=runtime/default`, but that
  is Docker/containerd spelling: Podman treats any value other than
  `unconfined` as a file path, so every Linux Job Pod failed to start with
  `opening seccomp profile failed: open runtime/default: no such file or
  directory` (all five M2-T6 probes and the M2-T4b exec probe). The flag now
  points at the containers-common default profile
  (`/usr/share/containers/seccomp.json`), which fails closed if absent.

- **fix(deps): golang.org/x/net v0.41.0 → v0.55.0** — patches
  [GHSA-5cv4-jp36-h3mw](https://github.com/tcs76321/Athanor/security/dependabot/1)
  (CVE-2026-25680, moderate): `net/html` parsing of arbitrary HTML
  can consume excessive CPU (denial of service). This is Athanor's
  Reader Mode pipeline exactly — `internal/gateway/reader.go` and
  `markdown.go` parse untrusted fetched HTML with `golang.org/x/net/html`.
  The upgrade also pulls the companion `x/sys` v0.45.0 and `x/text`
  v0.37.0 bumps. No new direct dependencies; `x/net` moves from
  indirect to a direct require (it was already a direct *import* —
  `go mod tidy` normalized the classification). `make check` green;
  Gate G1 (including the rule-6 outbound-HTTP walk) re-proven.

### M4 — Airlock & Gateway (complete; Gate G4 closed)

- **M4-T8 (this close-out).** The adversarial security suite lands in
  four commits (`1ab9ba5` SSRF, `7939b0f` allowlist bypass,
  `1fc3f22` content attacks + scanner hardening, this docs commit +
  behavioral probes). [demo-m4-t8.md](docs/demo-m4-t8.md) is the
  walkthrough and the Gate G4 evidence. The corpora run in CI with
  zero skips: SSRF (every deny-listed CIDR at its boundaries, DNS
  rebinding across the M4-T7 redirect hops, integer-host
  obfuscation at two layers, the `insecureSkipPrivateIPGuard`
  production-wire regression ADR-0017 §4 promised), allowlist
  bypass (typosquat / userinfo / trailing-dot / scheme smuggling /
  CRLF / IDN, with positive controls and per-denial audit
  assertions), and content attacks (10-payload injection corpus
  fail-closed, a ~10 MiB gzip bomb truncated at the streamed cap, a
  redirect-loop bomb bounded by the hop cap). The corpus found and
  fixed two real scanner weaknesses on its first run: the
  role-reassignment pattern was start-of-text anchored (any earlier
  attacker line evaded it — now multiline), and the base64 detector
  required a clean whole-run decode (chained padded blobs evaded it —
  now shape+size gated, fail-closed). Behavioral probes (gated
  `ATHANOR_RUN_INTEGRATION`) pass against the real internet:
  default-deny refuses a real domain; an allowlisted real domain
  fetches + extracts end-to-end (reference run 2026-09-15).
  **Gate G4 is closed**: adversarial suite green + research goal
  end-to-end on allowlisted domains (`TestDiverge_InjectsResearchIntoCandidates`,
  [demo-m4-t7.md](docs/demo-m4-t7.md) daemon walk). M4 (Airlock &
  Gateway) is complete; the next milestone is M5 (Context Engine).

- **M4-T7 (this close-out).** Gateway-backed tools `fetch_url` /
  `search_web` land in six commits (`f459ced` ADR-0019 + plan,
  T7.2 envelope widening + `search_engine_url_template`,
  `b712530` routes + runner + Gate G2, `b787982` redirect hop loop +
  adapter, T7.5 engine research sub-step + §7 amendment, this docs
  commit). [ADR-0019](docs/adr/0019-gateway-tools.md) records the
  decisions; [demo-m4-t7.md](docs/demo-m4-t7.md) is the operator
  walkthrough and the Gate G4 research-goal evidence. Decisions worth
  remembering: the tools are internal API routes dispatched through a
  `ToolGateway` inversion seam (the Core executes the fetch — Job
  Pods have `--network=none`, so the envelope override on a task is
  exactly the "allowlisted internet" capability); the closed set grows
  to six tools but `job_pod.default_tools` stays empty (per-task
  override only); redirects moved inside `Client.Fetch` as a bounded
  hop loop (max 5) with per-hop `Policy.Eval` + rate-limit + pinned
  dial — the first-hop-only policy evaluation was a latent SSRF
  bypass (an allowlisted host could 302 the gateway anywhere), closed
  and regression-tested by `TestFetch_DeniesOffListRedirect`;
  `search_web` is inert until the operator sets a `text/template`
  engine URL and the template host gets no allowlist exemption; tool
  responses are prompt material, so raw bytes are never inlined
  (reader refusals degrade to `mode=raw`; injection-flagged content
  fails closed with 422); a truncated extraction re-fetches once at a
  halved cap (the ADR-0018 deferred decision). The engine research
  sub-step fetches the task's declared URLs before divergence and
  injects attributed markdown into every candidate — soft-fail per
  source. ADR-0019 §7 was amended during implementation: the planner
  output is discarded free text, so sources are task-declared URLs
  (nothing scraped from LLM prose), and transport failures on every
  source no longer escalate. New event names: `research_start` /
  `research_fetch` (`jobs` category, open set — no migration). Gate
  G1 rule 6 re-proven (the adapter builds fetch requests with
  `gateway.NewRequest`); Gate G2 extended with the route-existence
  test and the two new tools in the envelope-bypass corpus.

- **M4-T6 (this close-out).** §21.5 responsibility 6, Reader Mode
  extraction, lands in three commits (`5928d45` deps + ADR-0018,
  `1dc946a` extraction layer, `ea5a9a1` config activation + daemon
  wiring). `internal/gateway/reader.go` is the closed pipeline
  `Client.Fetch → content-type gate → readability.FromReader →
  bluemonday UGCPolicy → renderMarkdown → prompt-injection scan`
  ([ADR-0018](docs/adr/0018-reader-mode.md)). Decisions worth
  remembering: we depend on the *maintained*
  `codeberg.org/readeck/go-readability/v2` v2.1.2 because the
  go-shiori module ARCHITECTURE §2 originally named is deprecated
  upstream; `FromURL` is never called (the gateway stays the single
  fetch point); the extraction *never* falls back to raw HTML; and the
  final markdown passes through the in-tree
  `scanner.PromptInjectionHeuristic` (MinLength=1, same as
  `cmd/athanor/ingress.go`) — a non-clean verdict returns
  `ErrPromptInjection` and no markdown leaves the reader. Fail-closed
  paths: non-HTML/text Content-Type → `ErrNotReadable`; a JS-only page
  (no main content) → `ErrNoReadableContent`; `reader_mode_default:
  false` → `ErrReaderDisabled`. Two new `network` event names,
  `reader_mode_applied` and `reader_mode_rejected`, join the open
  event set (no migration). `renderMarkdown` (`markdown.go`) maps the
  sanitized tree to a closed markdown subset (headings, paragraphs,
  nested lists, http(s)-only links, fenced/inline code, blockquote,
  hr, tables) and is table-tested. The dormant
  `network.reader_mode_default` flag (ADR-0017 §6, `f309500` pattern)
  is now effective: `config.Network.ReaderMode()` resolves it and
  `cmd/athanor/gateway.go:startGateway` returns a `GatewayParts` bundle
  (Client + Reader) constructed at boot. Test evidence: `make check`
  green with the new deps and transitive `golang.org/x/net` v0.41.0 /
  `golang.org/x/sys` v0.33.0 upgrades (Gate G1 re-proven — the M4-T1
  `O_NOFOLLOW` wrappers compile and pass); ~25 new `Test*` functions
  in `internal/gateway/{reader,markdown}_test.go` covering the hostile
  script/iframe/object/form/onerror corpus, the occlusion corpus, the
  text/plain path, the injection fail-closed, the disabled flag, and
  the end-to-end httptest fetch→extract seam T7 will sit on;
  `internal/config/config_test.go` pins `ReaderMode()` default-true /
  explicit-false. ADR-0018 §5 and §6 record the flag + audit
  contracts.

- **M4-T5.4 (this commit).** Gate G1 rule 6 per
  ADR-0017 §2: an AST walk in
  `TestGateG1NoOutboundHTTPOutsideGateway`
  (`internal/gate/gate_test.go`) fails the build
  if any non-test production source under
  `internal/` or `cmd/` references the
  package-level outbound HTTP helpers
  `http.Get`, `http.Post`, or `http.DefaultClient`
  outside the §21.5 gateway package
  (`internal/gateway/`) or the four
  `cmd/athanor/cli*.go` loopback-CLI files
  (`cli.go`, `cli_control.go`, `cli_export.go`,
  `cli_project.go`). The walk is a separate test
  function so the violation counter is per-rule;
  a future contributor who adds a single bad
  call site gets a single, focused failure. The
  inner walk is extracted into the
  `checkOutboundHTTP` helper, which the
  `TestCheckOutboundHTTP` unit test exercises
  against synthetic source: 3 forbidden-selector
  rows (`Get`, `Post`, `DefaultClient` — each
  caught independently), 4 positive controls
  (constructed `*http.Client` with explicit
  timeout, comment-position text, string literal,
  `http.MethodGet` / `http.MethodPost` constants
  — all correctly not flagged), and 1
  multi-violation row (all three caught in source
  order, sorted on output). The 8 subtests prove
  the rule actually catches violations; a
  regression that silently no-ops the walk (e.g.
  always returns nil) trips the unit test even
  if the integration test continues to pass
  against a known-good tree. Companion tests in
  `internal/gate/gate_relpath_test.go` pin the
  rule's data: `TestRule6ForbiddenIdentsPinned`
  pins the forbidden-ident set to
  `{DefaultClient, Get, Post}`;
  `TestRule6AllowedLocationsPinned` pins the
  allowlist to the four CLI files;
  `TestIsUnderGatewayPackage` exercises the
  prefix-match helper across the seven cases the
  walk relies on. The rule uses the same
  `ast.Inspect` shape as the syscall walk in
  rules 3/5 so a reader who knows one rule knows
  the other. Constructed `*http.Client` usage
  (e.g. the LLM client in `internal/llm/client.go`,
  the Job Pod runner in
  `internal/internalapi/runner/httpclient.go`) is
  not in the rule: those callers construct
  explicit timeouts and are sanctioned for
  loopback / Ollama use. The rule targets the
  "I'll just use http.Get" anti-pattern, not the
  explicit-timeout pattern. `make check` clean;
  all 7 gate tests green; Gate G1 strengthened.
- **M4-T5.2 (`225ed38`).** Gateway runtime per
  [ADR-0017](docs/adr/0017-gateway.md). The
  `Client` interface in `internal/gateway/client.go`
  is the §21.5 *only* outbound HTTP surface;
  consumers (M4-T7 tools, M6 credential broker)
  take the interface, not the concrete type, so
  a fake is substitutable in tests. `Client.Fetch`
  orchestrates the full §21.5 flow: `Policy.Eval` →
  per-host token-bucket rate-limit check → fixed
  deny-list header sanitizer (Cookie, Authorization,
  Proxy-Authorization, X-Api-Key, X-Auth-Token,
  X-Secret stripped by name; User-Agent set to a
  fixed Athanor UA) → per-request transport with
  `DialContext` pinned to the IPs the policy
  resolved (the §21.5 DNS-rebinding defense at
  the TCP layer) → streamed response size cap
  (truncate + flag, do not fail) → one `network`
  event appended under `category=network` with
  the full outcome (`fetched` / `truncated` /
  `denied` / `private_ip` / `rate_limited` /
  `error`). The daemon wires the gateway at boot
  via `cmd/athanor/gateway.go` (calls
  `gateway.NewPolicy` from `cfg.Network` and
  `gateway.NewClient` with the production
  `NetworkResolver{}` zero value that delegates to
  `net.DefaultResolver`); failure to construct is
  fatal so an operator who fat-fingers the config
  sees the error at startup, not at first fetch.
  ~14 `Test*` functions cover the Fetch flow
  against `httptest`: happy path, header stripping
  on the wire, fixed User-Agent, oversized
  response truncation, rate-limit denial,
  policy-short-circuit (transport panics if
  invoked — proves the policy runs first),
  private-IP refusal, invalid URL forms, nil
  request, transport error, audit shape (the
  on-the-wire `network` event), pinned-IP dial
  end-to-end (the `httptest` server actually
  receives the request through the pinned dial),
  `NewClient` validation. Total test count in
  `internal/gateway` after T5.2: ~30 `Test*`
  functions, all passing under `-race`. The
  `Policy` exposes a `PolicyOptions.InsecureSkipPrivateIPGuard`
  field that is **test-only**: the production
  `NewPolicy` shorthand never sets it, the daemon
  wire-up never sets it, and a future M4-T8
  regression test will assert the field is `false`
  in production. The flag exists so unit tests can
  dial `httptest` servers bound to 127.0.0.1
  without weakening the production §21.5 §4
  guarantee. Zero new dependencies; the project's
  dep list stays at two (`mattn/go-sqlite3`,
  `gopkg.in/yaml.v3`). Reference run on macOS 14,
  `make check`: `golangci-lint` 0 issues, `go vet`
  clean, `go test -race ./...` all green; Gate G1
  re-proven.
- **M4-T5.1 (`ab082e8`).** Gateway package
  skeleton at `internal/gateway/`. ADR-0017
  documents the §21.5 architecture: `internal/gateway/`
  is the only outbound HTTP door, Gate G1 rule 6
  (added in a follow-up T5.4) will fail the build
  if any other package uses `http.Get` / `http.Post` /
  `http.DefaultClient`, the §21.5 responsibilities
  1–5 land in T5.1/T5.2 and responsibilities 6
  (Reader Mode) and 7 (cloud-inference mediation)
  are explicitly deferred (T6 and M6 respectively).
  T5.1 ships the pure `Policy` value type, the
  `Resolver` interface (so tests inject a
  hand-written map; production wires
  `NetworkResolver{}` → `net.DefaultResolver`), the
  `Eval` method (URL parse → ASCII check → denylist
  → allowlist with suffix-match + explicit `.`
  separator → DNS-rebinding guard via the injected
  resolver), the `matchHost` helper (the
  `evilwikipedia.org` gotcha fix), the
  `nonRoutableCIDRs` deny-list (RFC 1918, loopback,
  link-local, CGN, multicast, unspecified,
  documentation/reserved — parsed once at package
  init via `mustParseCIDRs`), the closed `Decision`
  enum, the five sentinel errors
  (`ErrDeniedOffList`, `ErrDeniedDenyList`,
  `ErrDeniedPrivateIP`, `ErrIDNNotSupported`,
  `ErrInvalidURL`), and ~13 `Test*` functions that
  pin the suffix-match gotcha, the IDN ASCII-only
  constraint, every non-routable CIDR range as its
  own row, the deny-list override, the
  `default_policy: allow` + empty allowlist escape
  hatch, and the "default-deny never consults the
  resolver" durability property. The ASCII-only
  constraint is a deliberate T5 limitation; IDN is
  a T6+ concern that requires the
  `golang.org/x/net/idn` package — see the
  Forward references section of ADR-0017. `make
  check` clean; Gate G1 re-proven.
- **M4-T1 (`5f829b7`).** Path containment library at
  `internal/airlock/paths`. Three layers: `Resolve` (path
  arithmetic — absolute, traversal, NULL bytes), `Validate`
  (mode + structural — device/FIFO, setuid/setgid, symlink
  escape, unexpected executable), and `OpenNoFollow` (kernel —
  `O_NOFOLLOW` defeats a symlink at the final component,
  closing the Lstat→open TOCTOU window). The `O_NOFOLLOW`
  constant is reached through build-tag-gated wrappers
  (`paths_linux.go`, `paths_darwin.go`) that are the only
  files in `internal/` permitted to import `syscall`, gated
  by Gate G1's rule 5 (added in `54d8d7c`). The
  cross-platform fallback `paths_other.go` does not import
  `syscall` and emits a documented warning on unsupported
  GOOSes — the build, not the runtime, is the source of
  truth for which platforms get the kernel-level defense.
  `TestAdversarialCorpus` is the table-driven proof that
  every rejection class in `errors.go` is reachable from
  documented inputs (absolute path, traversal up, traversal
  via subdir, NULL byte, escape symlink, device FIFO, setuid,
  setgid, executable by default, executable with
  `AllowExecutable`, legal file, legal nested file, inner
  symlink with a legal target, missing with `AllowMissing`,
  missing without `AllowMissing`). The setuid / setgid rows
  self-skip on hosts whose filesystem silently drops the
  bit on `chmod` (notably macOS user directories); the
  probes are `modeReportsSetUID` / `modeReportsSetGID` in
  `paths_helpers_test.go`, and the production code is
  unchanged. `TestResolveRejectsUnsafeInputs` is a
  `testing/quick` property test (200 random samples) that
  asserts every (root, rel) input with `rel` starting with
  `/` or `..` returns `ErrAbsolute` or `ErrTraversal`.
  `TestResolveRoundTrip` asserts every legal (root, rel)
  pair produces a path in canonical `Clean` form that
  starts with `root`. `TestOpenNoFollowRejectsFinalComponentSymlink`
  is the behavioral test for the kernel-level defense
  (skips on GOOSes where `noFollowFlag` returns 0). Reference
  run on macOS 14, `make check`: `golangci-lint` 0 issues,
  `go vet` clean, `go test -race ./...` all green; Gate G1
  re-proven (`internal/gate/gate_test.go`).
- **Gate G1 rule 5 (`54d8d7c`).** Preparatory commit for
  M4-T1: Gate G1's AST walk now allowlists
  `internal/airlock/paths/paths_linux.go` and
  `internal/airlock/paths/paths_darwin.go` (full
  repo-relative path, not basename, so a future contributor
  cannot create an unannotated
  `internal/somethingelse/paths_linux.go` and slip through)
  and `allowedInternalSyscallIdents` is a closed set
  containing exactly one identifier, `O_NOFOLLOW`. The gate
  walks every `syscall.X` selector in those two files and
  asserts it is allowlisted — the same shape as the cmd/
  signal-constant check, applied to `internal/`. The
  `gate_relpath_test.go` companion pins the `relPath`
  helper's normalization contract and the sorted
  `allowedInternalSyscallIdentsKeyList` output. The actual
  library ships in `5f829b7`.
- **M4-T4 (`3b41b89`).** Egress pipeline per
  ADR-0015. `internal/airlock/egress` subscribes
  to the append-only event log for
  `category=artifact, event=accepted` rows, runs
  the egress scanner registry (size + zipbomb +
  clamav + yara — no prompt-injection, see the
  T3 close-out), validates the export tree
  through `airlock/paths`, and copies a clean
  export to `<state>/workspace/exports/<project>/<artifact>-<sha12>/`.
  The exporter is best-effort and asynchronous
  (poll on `airlock.egress_poll_interval`, default
  5s); the engine's `artifact_accepted` event is
  durable in the artifact store *before* the
  event row is appended, so a crash between
  append and export means the next daemon boot
  re-exports on the next poll cycle. The export
  is idempotent: the SHA-12 suffix is the content
  hash, so re-writing the same export is a no-op.
  The same `/internal/v1/exports/<id>` API serves
  the manual `athanor export` CLI subcommand
  synchronously. `internal/api.SetManualExporter`
  is the seam between the asynchronous pipeline
  and the synchronous CLI; the interface keeps
  the `internal/api` package free of the egress
  import (Gate G1 keeps the dependency graph
  narrow). `make check` clean.
- **M4-T3 (`28e53cc`).** Pluggable scanners. The
  `Scanner` interface in `internal/airlock/scanner`
  is pure, streaming-aware, and explicit-verdict
  (`VerdictClean` / `VerdictUncertain` /
  `VerdictRejected`); the per-pipeline registry's
  `RunAll` returns the most-severe verdict across
  all scanners. In-tree scanners: `size`
  (configurable byte cap, default 100 MiB),
  `zipbomb` (decompression ratio + entry count,
  defaults 100x and 10k), `heuristic` (the
  prompt-injection keyword scanner, gated on a
  2 KiB long-prompt threshold; only the
  `PipelineUserPrompt` pipeline uses it). The
  ClamAV and YARA adapters live in
  `cmd/athanor/scanners/`; Gate G1's AST walk now
  allowlists that directory for `os/exec` (the
  same shape as the M2 production Podman client's
  named-file exception, extended from a single
  file to a directory — see the `54d8d7c` /
  `28e53cc` allowlist evolution). Scanner-absent
  at construction degrades to `VerdictUncertain`
  at scan time: a named scanner the registry
  cannot instantiate (e.g. "clamav" without a
  `clamdscan` binary on PATH) is fail-closed, so
  the pipeline refuses rather than silently
  passes. The M3-T1 ADR-0015 four-crossing table
  is the durable record of "which scanners run
  where": ingress is the full set (heuristic +
  size + zipbomb + clamav + yara), egress
  intentionally omits the prompt-injection
  scanner (LLM-generated data is not adversarial
  to itself), and `user_prompt` is the heuristic
  only over the 2 KiB threshold. `make check`
  clean.
- **M4-T2 (`0f727b0`).** Ingress pipeline per
  ADR-0015. `internal/airlock/ingress` watches
  `<state>/workspace/inbox/` with `fsnotify`,
  routes new files through `airlock/paths` for
  path-containment (T1) and the per-pipeline
  scanner registry for byte + zipbomb + (optional)
  prompt-injection scanning, then disposes to
  `.processed/` (clean) or `quarantine/` (rejected
  or uncertain). The `quarantined_files` table
  (migration 0008) records every quarantine with
  the deciding scanner's reason keyed by SHA-256;
  the `QuarantineRepo` exposes `Put` / `Get` /
  `ListByReason` for the operator's audit
  workflow. The watcher is bound to the daemon's
  lifetime; deferred `Close` drains the pending
  queue before the process exits. The same scanner
  registry is reused for the egress pipeline (T4)
  — one registry, three pipeline selections. The
  audit row carries a synthetic `event` key (the
  row's primary dispatcher: `accepted`,
  `quarantined`, `rejected`, `duplicate_ignored`)
  so a SQL filter on `events.data_json` can find
  rows without parsing JSON. `make check` clean.

### M3 follow-ups

- **`Repository.Transition` race guard (`b9f0bee`).** When the
  in-memory read observes the same state another writer just
  committed (`current.State == to`), the function returns
  `ErrConcurrentTransition` instead of `IllegalTransitionError`.
  The §8.2 state machine has no self-loops, so the only way
  this happens in production is a race; the loser of two
  concurrent transitions now consistently sees the race error
  rather than a self-loop error. The pure `ValidateTransition`
  function is unchanged (its `state_test.go` self-loop
  assertions still hold). Sibling test
  `TestConcurrentTransition_DifferentTargetsRacesCAS`
  exercises the SQL CAS branch on a different-target race so
  both layers are pinned.

- **`Engine.Enqueue` concurrency cap is now atomic (test-only
  symptom: `TestEnqueueRespectsConcurrencyCap` flaky).**
  The polling loop did `LoadInt64(&inFlight) < max` then
  `AddInt64(&inFlight, 1)` as two separate ops; under
  contention two goroutines could both pass the check and
  both increment, silently exceeding the live cap. Replaced
  with a single `CompareAndSwapInt64` so reservation is
  atomic: the second CAS observes the first increment and
  retries. The live-cap behavior (re-read on every poll,
  mid-flight profile changes honored) is unchanged; the
  polling ticker still backs off 50 ms when the cap is full.
  `TestEnqueueRespectsConcurrencyCap` (M1-T8.4) is the
  pinned contract.

- **`MinJudgeConfidence = 0` end-to-end (`174f3a4`).** The
  §19.3 deterministic guard is now reachable as a disabled
  mode through config. The field is now `*float64` (mirroring
  the existing `*bool` pattern) so the operator can
  distinguish "unset" (use the default 0.7) from "explicitly
  zero" (disable). A previous version of
  `internal/engine/compare.go` overrode an explicit 0 back to
  0.7 inside the engine, making the disabled mode unreachable;
  that override was removed and the rescue in
  `internal/config/defaults.go` was updated to honor the
  pointer. New helper `Execution.MinJudge()` returns the
  resolved float64 with the default applied. Tests in
  `internal/engine/compare_threshold_test.go` (end-to-end
  through `phaseCompare`) and `internal/config/config_test.go`
  (load layer in isolation) pin the contract in both layers.

- **Dormant `Execution` flags marked deferred (`f309500`).**
  `require_tests_for_code`, `require_documentation_for_code`,
  and `compare_before_accept` are declared + defaulted to
  `true` so the shipped example config validates and parses,
  but the engine does not yet consult them. Operators who
  set any of these to `false` today will see no behavior
  change; they become effective in M6/M7. The deferral is
  documented inline in `config.example.yaml` and
  `ARCHITECTURE.md` so operators are not silently lied to.

- **`e.cfg` nil guards in engine (`834b8a8`).** Defensive
  `e.cfg == nil` checks added in `phaseCompare`,
  `phaseDivergeN`, and the engine's `call` helper, matching
  the existing guard in `resolveMaxReflectionLoops`. No
  production path constructs an Engine with `nil` cfg, so
  this is purely defensive consistency; the guards surface a
  clear error rather than a nil-pointer panic if a future
  caller makes that mistake.

- **`DecideWinner` always returns non-empty `Reasons`
  (`7b787d5`).** The pure function now appends a one-line
  engine annotation to the `Reasons` slice on every branch
  (disabled, no-record, satisfied, downgrade). The `comparison`
  audit row the engine writes is then guaranteed to have
  something to read in a post-mortem, even when the LLM
  returned an empty `reasons` array. Existing
  `TestDecideWinner` table tests still pass; the new
  `TestDecideWinner_ReasonsAlwaysNonEmpty` pins the
  post-mortem-readability contract across all five branches.

- **Integration-test gating documented (`ccbda5c`).** A new
  "Integration (behavioral) security probes" section in
  `DEVELOPMENT.md` explains why the five behavioral probes
  in `internal/jobpod/security_test.go` are gated by
  `ATHANOR_RUN_INTEGRATION=1` and skipped in CI. The
  structural argv regression test and the LLM-isolation
  tests run in CI; the integration probes run on a
  developer's machine with a real `podman` runtime. The
  reference run on 2026-08-30 (macOS 14 / podman 5.8.2 /
  applehv) passed all five.

### M3 — Dialectical Loop v1 (started)

- **M3-T1 (close-out, `62ae865`).** Four follow-up fixes landed
  after the M3-T1 phase executor was merged: (1) `phaseReflect`
  re-fetches the Job before reading the reflection counter — the
  `j` parameter passed to phase handlers is a snapshot taken at the
  top of the `Run` loop, so the in-memory `RecoveryFlag` lags the
  DB by one iteration; (2) `engine.Run` no longer clears
  `RecoveryFlag` values prefixed with `"reflect-"`, so the reflection
  counter survives between `Run` iterations (otherwise the counter
  resets to 0 after every successful step and the budget check
  never fires); (3) `phaseEvaluate` is now idempotent — candidates
  that already have an `EvaluationRecord` for this job are skipped,
  preventing re-evaluation of past cycles' proposals and making
  the test-time verdict queue sized correctly; (4) `phaseReflect`
  enforces the budget *after* bumping the counter but *before*
  re-entering `diverging`, so the budget check fires immediately
  on the last allowed iteration rather than wasting a divergence +
  evaluation cycle. The E1/E2/E4/E5 dialectical-loop test suite
  (`internal/engine/multicandidate_test.go`) lands with the fix:
  3-candidate happy path, all-fail reflection budget exhaustion,
  §19.3 comparison downgrade to `previous`, and the
  no-passing-candidate `failed` path. All assertions exercise
  `phaseCalls()` to prove the per-phase LLM call count, proving
  the §8.2 state machine structure rather than just the end
  state. Reference run on macOS 14, fake Ollama, `make check`:
  `golangci-lint` 0 issues, `go vet` clean, `go test -race ./...`
  all green, Gate G1 (`internal/gate/gate_test.go`) and Gate G2
  (`internal/gate/gate_g2_test.go`) both pass.
- **M3-T1 (`9cb4ccd`).** Full §8 phase executor in
  `internal/engine`. Six phases wired to the §13.1 Dialectical
  Loop: `phasePlan` (tall persona, temp 0.2), `phaseDivergeN`
  (main, temp 0.7–1.1, N candidates per `cfg.Execution.
  DivergenceCandidates`), `phaseEvaluate` (security, temp 0.0,
  per-candidate `EvaluationRecord` persistence), `phaseReflect`
  (main, temp 0.6–0.8, budgeted retry), `phaseSynthesize` (main,
  temp 0.2, with the M2-T4 `code` archetype sub-steps), and
  `phaseCompare` (security, temp 0.0, with the §19.3 deterministic
  guard downgrading an LLM `winner: new` verdict when no
  `EvaluationRecord` has `better_than_previous: true` with
  confidence > `min_judge_confidence`). Each phase persists its
  transition before the next runs (§8.2). The recovery-flag
  counter for reflection is co-opted on `RecoveryFlag` with a
  `"reflect-N"` prefix; M3-T4 will move it to a typed counter in
  `system_state` (per the comment in `internal/engine/reflect.go`).
  Tests: `internal/engine/engine_test.go` (M1 path still
  green) and `internal/artifact/store_test.go` (new
  `LatestAcceptedByProject` query for §19.3).
- **M3-T1 (`9e99e20`).** `EvaluationRecord` schema and repository.
  New table `evaluation_records` (migration 0007) with the
  §19.2 field set: artifact_id, compared_against, score,
  passed_tests, failed_tests (JSON list), missing_criteria (JSON
  list), security_issues (JSON list), style_issues (JSON list),
  better_than_previous, confidence, summary, created_at. The
  repository (`internal/evaluation/repo.go`) writes the row and
  the audit `events` row in one transaction so a crash between
  data and audit cannot leave a record without its footprint
  (mirrors the §8.2 transition pattern and the
  `artifact.NewVersion` pattern). List-by-job ordering is
  deterministic (created_at ASC, id ASC tiebreaker). The package
  imports only `store` and `ids` — no engine, job, artifact, or
  project dependency, so it cannot form an import cycle and a
  unit test can exercise it without booting any other package.
  Tests: `internal/evaluation/repo_test.go` (Create, Get,
  ListByJobOrdering, ListByJob across multiple jobs).
- **M3-T1 (`c73a115`).** Full §8.1 state machine and transition
  table. The 13 §8.1 job states are all accepted by the schema
  (migration 0004 was the CHECK constraint; this commit
  wires the Go-side `CanTransition` table to match). The engine
  dispatches every state to a dedicated phase handler in
  `phases.go`; every transition is committed before the next
  phase runs (§8.2 crash safety). The phase graph is now:
  `queued → context_building → planning → diverging (N) →
  evaluating → [reflecting ↔ diverging]* → synthesizing →
  comparing → completed | failed`, with the `paused` terminal
  for §22 kill-switch and `awaiting_approval` (M6) reachable
  from `comparing`. `internal/job/state_test.go` covers the
  full transition table (legal moves accepted, illegal moves
  rejected). `internal/engine/recovery_test.go` proves a
  kill-mid-phase resumes from the last committed state.

- **M3-T2 (close-out, 6 commits).** The evaluation phase
  ships end-to-end: the §19.1 rubric drives per-archetype
  acceptance checks, the new `/internal/v1/jobs/{id}/lint`
  route extends the tool envelope (Gate G2 re-proven), the
  §19.3 deterministic guard is now a pure function
  (`DecideWinner`), the per-phase wall-time budget emits a
  `context_deadline_exceeded` audit row, and the
  per-task probe in `spikes/m3-t2-probe/` exercises 5
  code-archetype goals against the rubric. Architectural
  decisions are recorded in `docs/adr/0011–0014`. The M2-T4
  pod sub-steps that lived in `phaseSynthesize` are gone
  (tests now run once per candidate in `evaluating`); see
  ADR-0014 for the rationale. `make check` is clean: `golangci-lint`
  0 issues, `go vet` clean, `go test -race ./...` all green;
  Gates G1 and G2 re-proven. Per-task commits:

  - `M3-T2: drop M2-T4 pod sub-steps from phaseSynthesize (ADR-0014)` — `a4e2362`
  - `M3-T2: per-archetype evaluation rubric (commit 2.2)` — `4d81540`
  - `M3-T2: per-task linter via /internal/v1/jobs/{id}/lint route + ruff adapter (commit 2.3)` — `f09380d`
  - `M3-T2: per-phase wall-time budgets apply to LLM calls (commit 2.4)` — `baa86bd`
  - `M3-T2: extract §19.3 comparison as DecideWinner pure function (commit 2.5)` — `bc68ebc`
  - `M3-T2: per-task probe — 5 code goals, rubric coverage report (commit 2.6)` — `97aed91`

- **M3-T3 (close-out, 4 commits).** Comparison phase
  hardening: (1) the comparison prompt now includes a
  "Previous-record summary" section so the LLM judge can
  calibrate "better than previous" against how the previous
  itself scored (commit 3.1); (2) the supersede + accept
  transition is now atomic via `artifact.Store.SupersedeAndAccept`
  — a crash mid-swap can no longer leave a project with
  zero accepted artifacts, and the §9.3 status flow gains
  the `accepted → superseded` edge (commit 3.2);
  (3) `parseComparisonVerdict` trims whitespace before the
  closed-set check and reports unknown winners via a typed
  `errUnknownWinner` (no more silent downgrade to "none"),
  with the engine auditing the downgrade as
  `comparison_unknown_winner_downgraded` (commit 3.3);
  (4) the `DecideWinner` table tests gain 6 new rows
  covering the upper-boundary case (confidence just above
  threshold), multi-record ties, and the explicit
  non-promotion of "previous" / "none" verdicts even when a
  record would back "new" (commit 3.4). `make check` clean;
  Gates G1 + G2 re-proven. Per-task commits:

  - `M3-T3: comparison prompt includes previous-record summary` — `2fbae3c`
  - `M3-T3: atomic supersede+accept via SupersedeAndAccept` — `ce24cca`
  - `M3-T3: trim comparison winner; audit unknown-value downgrades` — `e6b487f`
  - `M3-T3: DecideWinner table tests for ties + boundaries` — `a5da467`

- **M3-T4 (close-out, 3 commits).** Reflection phase
  hardening: (1) the reflection counter is now a typed
  `system_state` row (`reflect:counter:<job-id>`) instead
  of co-opting `jobs.recovery_flag` with a `"reflect-N"`
  string; the `engine.Run` `HasPrefix("reflect-")` guard
  is gone (commit 4.1); (2) the budget is now a config
  field (`execution.max_reflection_loops`, default 2) read
  via `e.resolveMaxReflectionLoops` instead of a hard-coded
  constant (commit 4.2); (3) `validateCross` rejects
  non-positive `max_hard_task_variations` and
  `max_reflection_loops` values at config load (commit
  4.3). `make check` clean; Gates G1 + G2 re-proven.
  Per-task commits:

  - `M3-T4: typed reflection counter in system_state` — `6956a1f`
  - `M3-T4: Execution.MaxReflectionLoops config field` — `dbe3478`

- **M3-T5 (1 commit).** Widens the closed tool
  envelope to include `git_operation` for the
  M3-T7 work that records accepted artifacts to a
  project-local git repo. The actual call site is
  M3-T7 work; this commit only widens the closed
  set and the matching test. Per-task commit:

  - `M3-T5: git_operation tool in closed set` — `7ef6d34`

- **M3-T6 (1 commit).** Crash-recovery E2E coverage
  at the two phase boundaries the M1-T8 close-out
  did not exercise: `StateDiverging` (the job
  enters divergence but no candidate artifacts
  are persisted yet) and `StateEvaluating` (the
  job enters evaluating but no evaluation records
  are persisted). Per-task commit:

  - `M3-T6: crash-recovery at mid-diverging + mid-evaluating` — `d85a886`

- **M3-T7 (1 commit).** Dialectical-vs-single-shot
  quality probe scaffold: defines the
  `dialecticalResult` data contract and the
  loopback-HTTP client the three sub-measurements
  (T-a calibration, T-b stability at T=0, T-c
  diversity Jaccard) will consume. The actual
  experiments land in follow-up work. Per-task
  commit:

  - `M3-T7: dialectical probe scaffold (T-a/b/c contract)` — (this commit)

### M2 — Container Spine (continuing)

- **M2-T6.** Security test suite closes Gate G2. Two layers: a
  structural argv regression test
  (`TestGateG2JobPodArgvCannotEscape` in
  `internal/gate/gate_g2_test.go`) that runs in CI and
  grep-blocks `--net=slirp4netns`, `podman.sock`, and host-FS
  bind-mount sources from the argv source files; and five
  behavioral probes in `internal/jobpod/security_test.go`
  gated by `ATHANOR_RUN_INTEGRATION` that bring up real
  hardened pods and assert `wget` to the internet fails, both
  Ollama host aliases fail, the podman socket is absent,
  writes to the rootfs are read-only-denied with the pod's
  `/` showing an `overlay` mount, and the pod's env is
  free of secret patterns. Every probe appends a row to
  the `events` table (category `podman`) with the script,
  exit code, result, and elapsed ms — the audit trail
  satisfies the "failures logged as security events"
  acceptance. Decisions: ADR-0010. Plan: `docs/m2-t6-plan.md`.
  Evidence: `docs/demo-m2.md`. Reference run 2026-08-30 on
  macOS 14 / podman 5.8.2 / applehv: all five probes pass
  in ~7s combined; CI structural test green;
  `make test-race` clean. No new dependencies. Gate G2
  is fully green and M2 is complete; M3 (Dialectical Loop
  v1) is unblocked.
- **M2-T5.** Terminal-state cleanup + token-dir revocation
  on supervisor exit. `internal/jobpod/manager.go` removes
  the in-memory pod entry when the supervisor observes a
  terminal `podman inspect` result (`exited` / `stopped`).
  The token dir is unmounted in the same supervisor tick,
  so the per-job bearer secret never outlives the pod. The
  `make test-integration` target is added so the orphan-reap
  test (M2-T2's startup sweep) is opt-in per the existing
  convention. Three commits: `379a6c9` (sweep wiring),
  `5d13221` (test-integration target), `83f90e7` (terminal-
  state drop).

- **M2-T4.** Internal API `execute_code` and `run_tests` routes
  with a per-job tool envelope (ARCHITECTURE §25). Closed set of
  two tools lands in `internal/toolenvelope`; per-task override
  via `tasks.allowed_tools_json` (migration 0006); the handler
  enforces the envelope server-side and returns 403 with an
  audit event `tool_disallowed` on rejection. The engine
  decides when to call (sub-steps in `phaseSynthesize` for the
  `code` archetype; text/document/data/media skip them and
  complete via the M1 walking skeleton). The production
  `*internalapi/runner.HTTPClient` talks to the loopback
  internal API with the per-job bearer token retrieved from
  `jobpod.Manager.TokenFor(jobID)` — the engine is just
  another client of the same surface. Decisions: ADR-0009.
  Structural proof: Gate G2 extended in
  `internal/gate/gate_g2_test.go`
  (`TestGateG2ToolEnvelopeBypassImpossible` greps every
  handler file for `a.tools.EnvelopeFor`; the route-count test
  grows from 3 to 5 to cover the new routes). Behavior: 4
  commits (`chore: add tool envelope package + per-job
  allowlist config` `ef0f4b9`, `docs: ADR-0009 engine-pod-wiring`
  `f7674a3`, `M2-T4: internal API execute_code + run_tests
  routes with allowlist` `8ed7ee9`, `M2-T4: engine sub-step pod
  execution + production wiring` `843e123`). All
  internalapi + engine + project + gate tests green;
  `make test-race` clean; Gate G1 still passes. No new
  dependencies.
- **M2-T3.** Per-job tokens (16-byte random hex, mounted at
  `/run/athanor/token` via the existing bind-mount argv) plus
  the `/internal/v1/` HTTP surface behind a bearer-token auth
  middleware. Three routes in M2-T3 scope: GET job context, POST
  heartbeat, POST log. `crypto/subtle.ConstantTimeCompare` guards
  the bearer; per-job binding via `r.PathValue("id")` ensures a
  token for job A is rejected on job B. Decisions: ADR-0008.
  Structural proof: Gate G2 in `internal/gate/gate_g2_test.go`
  (no `internal/llm` import, `ConstantTimeCompare` present in
  middleware, every route wrapped in `authMiddleware`).
  Behavior: 6 commits (`1fc6116` token dir, `90938a9` auth
  middleware, `aa4e354` handlers, `18d684a` Gate G2, `ba54a49`
  daemon boot wiring, ADR-0008). All 26 internalapi tests +
  Gate G2 tests green; `make test-race` clean. No new
  dependencies.

### Foundation layer (F1–F6)

- **F6.** Initialize `CHANGELOG.md`. One line per M#-T# entry from now
  on; this section is the durable record of layer transitions.
- **F5.** Add `make bench` target and `BenchmarkRunFullChain`. Baseline
  numbers recorded in `docs/benchmarks/engine-m1.txt` (2.32 ms per M1
  chain on Apple M2 Max, fake Ollama, no network). M3 multi-candidate
  evaluation will be compared against this baseline.
- **F4.** Complete truncated M1-T8 sentence in
  `docs/probes/m1-quality-probe.md` and add a "What M1-T8 Did NOT
  Test" section that pre-declares the M3-T7 probe's scope. M3-T7
  must cover multi-candidate divergence, evaluating/reflecting phases,
  Job Pod test execution, multi-language code, large-context retrieval,
  hallucination rate, strategy-mining signal, HITL latency, daydreaming,
  and OS watcher behavior — none of which M1-T8 measured.
- **F3.** Document what `internal/gate/gate_test.go` proves and what
  it does not. Gate G1 is a structural containment guarantee (no
  tool-execution imports, no raw `syscall` in agent code, signal-
  constant-only `syscall` in `cmd/`, single named file allowlist for
  `os/exec`); it is not a behavioral guarantee against prompt
  injection or unsafe LLM outputs.
- **F2.** Replace prose README "Status" section with a structured
  three-block layout: "What works today (M0 + M1 + M2-T1 + M2-T2)",
  "What's next (M2 finish)", "What's deferred (M3..M7)". The block is
  updated as the final step of every layer transition.
- **F1.** Verify the `LICENSE` file is GNU AGPL v3 and surface it in
  the README preamble with a one-line `License: [AGPL-3.0](LICENSE)`
  badge. LICENSE was already present and referenced in
  `DEVELOPMENT.md`; this makes the license visible at a glance for
  any newcomer.

## Prior work (pre-changelog, summarized)

The 57 commits before F1 are summarized below by milestone. Full
history is in `git log`.

### M2 — Container Spine (in progress)

- **M2-T2.** `internal/jobpod` package: types/errors/Client interface,
  Manager impl (Start, Stop, Get, Supervise), Sweep on daemon boot,
  per-platform hardening flag builders (common + Linux seccomp +
  Darwin no-op), and tests. Wire the manager into daemon boot for a
  startup orphan sweep. Production `os/exec` client lives in
  `cmd/athanor/jobpod_client.go` (the only file allowed by Gate G1).
  Decisions: ADR-0007.
- **M2-T1.** Rootless Podman lifecycle spike (`spikes/podman-lifecycle`).
  Seven §21.2 containment checks pass on macOS 14 + podman-machine
  5.8.2 + applehv. ADR-0007 documents the seven checks, the macOS-
  only validation scope, and the seccomp caveat.
- **M1-T8.4.** Wire `PowerManager.MaxConcurrentJobs()` into the
  engine's concurrency cap. M1-T8 follow-up integrated.

### M1 — Walking Skeleton (code-complete)

- **M1-T8.** Quality probe against a live Ollama. Ran 2026-08-26
  with `gemma4:12b-mlx`; 5/5 acceptance-criteria adherence across
  text/code/document archetypes. Per-model timings and findings:
  `docs/probes/m1-quality-probe.md`. Three follow-ups integrated
  on 2026-08-26: M1-T8.1 (synthesis preamble suppression prompt
  fix), M1-T8.2 (default planning budget 120s → 300s), M1-T8.3
  (synthesizing-phase crash-recovery test).
- **M1-T7.** CLI client (`cmd/athanor/`): `project create`,
  `goal submit`, `job watch`, `artifacts`, `freeze`, `unfreeze`,
  `init`. HTTP API in `internal/api/`. End-to-end test
  `TestEndToEndWalkingSkeleton` is the Gate G1 demo.
- **M1-T6.** Kill switch (`internal/control`). Frozen state
  persists in `system_state` and survives restarts. Unfreeze
  requires a reason. Migration 0005.
- **M1-T5.** Artifact model (`internal/artifact`). Draft creation,
  supersede chain (linear, version-incrementing), SHA-256 content
  hashes, status flow per §9.3.
- **M1-T4.** Job state machine (`internal/job`). All 13 §8.1
  states accepted by schema (migration 0004). M1 transition table
  is the §8.2 subset that M1 actually exercises; `evaluating`/
  `reflecting`/`awaiting_approval` are reachable in M3/M6 per
  ADR-0001. CAS-style transitions per ADR-0006.
- **M1-T3.** Prompt assembly (`internal/prompt`). Deterministic,
  byte-identical output for identical inputs. §11.2 section order
  reserved; M5 fills slots 7-12. Per-section token accounting.
- **M1-T2.** Context feasibility check (`internal/llm/feasibility.go`).
  Floors are role-scoped, not task-scoped (ADR-0002). Pauses the
  job and emits a `context_floor_violation` event on violation
  rather than silently reducing context.
- **M1-T1.** Ollama client + persona registry. The five fixed
  roles (`wide`, `tall`, `main`, `security`, `alternative`) are
  resolved from configuration; missing any role fails boot
  loudly.

### M0 — Foundations

- **M0-T8.** CI: `vet` + `test -race` + `golangci-lint v2.13` on
  push. `make check` is the per-commit contract. `make hooks`
  installs the pre-push gate.
- **M0-T7.** Loopback-only HTTP server. `server.LocalhostAddr`
  rejects non-loopback hosts outright (fail closed).
- **M0-T6.** Append-only EventLog API (`store.AppendEvent`).
  `events` table is DB-enforced append-only via `BEFORE UPDATE/
  DELETE … RAISE(ABORT)` triggers (migration 0001). Query
  surface: `store.QueryEvents` with category/job/project filters.
- **M0-T5b.** Schema migrations 0001 (execution spine), 0002
  (learning/control), 0003 (canonical enum values per ADR-0005),
  0004 (job-state CHECK per ADR-0006), 0005 (system_state). All
  forward-only, backup-before-migrate, FK-checked at COMMIT.
- **M0-T5a.** SQLite store: `mattn/go-sqlite3` with CGO, single
  connection, WAL, `synchronous=NORMAL`, `busy_timeout=5000`,
  `foreign_keys=ON`. Decisions: ADR-0003, ADR-0004.
- **M0-T4.** Migration runner. Pre-migration backup via
  `VACUUM INTO`. Each migration in its own transaction with
  `PRAGMA foreign_key_check` gating the commit (per ADR-0006
  recipe).
- **M0-T3.** Category-tagged structured JSON logging to
  `state/logs/`. Per-category files with size-based rotation
  (10 MiB / 5 files). Closed set of categories from §28.1.
- **M0-T2.** Config loader: strict YAML, built-in defaults,
  `config.example.yaml` matches defaults (test-enforced). Custom
  `Duration` type that rejects bare numbers.
- **M0-T1.** Go module + project layout (`cmd/`, `internal/`,
  `migrations/`, `spikes/`, `docs/`). AGPL-3.0 license.


