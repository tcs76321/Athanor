# Known issues

A running log of findings from code review, with dispositions. This is not the
roadmap backlog (`ROADMAP.md` §7) — the items here are defects or hardening
gaps, not deferred features.

**Review:** 2026-10-06, static read-only review of `internal/` and `cmd/`
(no builds/tests run during the review). Findings verified by hand before
recording; each entry names its location and status.

**Review:** 2026-10-16, a deeper review of every subsystem (design and
implementation). High/medium findings were fixed in commits
`7bf9ca7`..`9c65b3b` (see "Fixed (2026-10-16 review)"); the remaining findings
are under "Open (2026-10-16 review)".

---

## Open (2026-10-16 review)

Deep review of every subsystem (`internal/` + `cmd/`), design and
implementation. Findings below are **unfixed** as of 2026-10-16 and ordered by
severity. Locations are the call sites that need attention; "suggested fix" is
advisory, not a contract. There are no open high items (H7 and H8 are fixed;
see "Fixed (2026-10-16 review)"); the rest are hardening.

### Medium

| # | Finding (location) | Why it matters / suggested fix |
|---|---|---|
| M1 | `PutSource` upsert does not update `job_id`/`project_id` on conflict (`internal/mce/store.go:130`) | content-derived chunk IDs mean re-ingesting the same bytes under a different scope keeps the first-writer attribution, so `IndexForJob`/`IndexForProject` completeness depends on ingestion order. Latent today (production only ingests project-scoped chunks). Correct fix is a multi-owner (join-table) model; a minimal fix is `job_id = excluded.job_id, project_id = excluded.project_id` with last-writer-wins documented. |
| M2 | Config validation accepts negative values for many bounded numeric fields (`internal/config/validate.go`; `limits.*`, `recovery.*`, `backup.max_local_backups`, `power.daydream_*`, `strategy_analysis.*`, `job_pod.pids_limit/memory_mb/cpus`, `network.rate_limit_per_minute`, `agent.max_proactive_tasks_per_day`) | a typo like `max_concurrent_jobs: -3` loads silently (only `0` is replaced by a default), violating the fail-loud contract. Add per-field range checks. |
| M3 | Fetched-content prompt-injection defense is only the regex heuristic; the "security-persona classifier" is not wired (`cmd/athanor/gateway.go:71` wires only `PromptInjectionHeuristic`; `internal/airlock/scanner/heuristic.go`) | `fetch_url`/`search_web` feed web content into prompts with ~10 regexes as the only guard. Wire a real classifier (or YARA) into `ReaderOptions`, or correct the heuristic's comment so the gap is visible. |
| M4 | Gate G2's envelope-bypass proof iterates a hand-maintained `toolHandlers` map that drifts from the `toolenvelope` closed set (`internal/gate/gate_g2_test.go:201`) | a new tool + route + handler that forgets its envelope check trips nothing. Derive the map from the `Tool` constants (an `AllTools()` exporter). |
| M5 | Gate G2's route-auth check string-counts `mux.Handle(` and misses `mux.HandleFunc` / wrapped sub-muxes (`internal/gate/gate_g2_test.go:150`) | a route registered via `HandleFunc` is invisible to the "every route is auth-wrapped" proof. Parse the registration AST or centralize through a single `wrap()` helper. |
| M6 | `O_NOFOLLOW` only closes the final-component symlink race; intermediate directory components are still followed between `Validate`'s `EvalSymlinks` and `open(2)` (`internal/airlock/paths/paths.go`) | defense-in-depth for a local single-user tool, but the doc comment overstates the guarantee. Use `openat2`/`RESOLVE_BENEATH` or walk-and-open component-wise; at minimum correct the comment. |
| M7 | Judge quorum votes share one seed/persona/prompt, so `judge_count > 1` aggregates near-identical outputs and provides no decorrelation (`internal/engine/phases.go:323`, `compare.go:344`) | undermines F4-T4's stated purpose. Vary the seed per vote (fold the vote index into `judgmentSeed`) or route each vote to a distinct model. |
| M8 | `decidePlan` overwrites a custom policy's `JudgeCount`, and the evaluation judge is always called exactly once (quorum applies only to comparison) (`internal/engine/engine.go:445`) | undocumented precedence; the "quorum applies in either mode" comment is only half true. Document or honor the policy's count for evaluation too. |
| M10 | `lineDiff` is an O(n·m) LCS matrix (`internal/ui/ui.go:614`) | a ~1 MB artifact diff (~20k lines) allocates ~3 GB and OOMs on a loopback GET. Use a memory-linear diff and cap input size. |
| M11 | `NewVersion` writes the content file inside the transaction; a rollback after the write (supersede CAS or audit failure) leaves an orphan file (`internal/artifact/store.go:100`, `persist.go:44`) | each retried version leaks a file. Write to a temp name and `os.Rename` after commit, or remove on rollback. |
| M12 | Power policy treats unknown battery % (`0`) as "above threshold", so deep work proceeds on battery when the reading is unavailable and override is on (`internal/power/policy.go:84`) | fail-open where pause is the conservative choice. Treat unknown as at/below threshold unless the operator opts into "assume AC". |
| M13 | Core Pod image boots with no config and the host-binary `ollama_url` default (`deploy/core.Containerfile:55,67`); the shape mismatch is only a doctor *warning* | a fresh Core Pod points Ollama at `127.0.0.1` inside the container where none exists. Bake a Core-Pod config (`host.containers.internal`) or make the shape mismatch a hard fail in the container context. |
| M14 | Strategy confidence-consistency baseline includes the cohort's own members (`internal/strategy/analysis.go:120`) | a large cohort is compared against a baseline dominated by itself. Compute the archetype baseline leave-one-cohort-out. |
| M15 | `Backfill` fabricates a `"legacy"` archetype for pre-capture jobs (`internal/strategy/strategy.go:313`) | a synthetic archetype distorts per-archetype baselines and can mint spurious insights. Exclude backfilled rows from mining or derive the real archetype. |
| M16 | `strategy_insights` lacks a UNIQUE constraint on `pattern_json` (`migrations/0021_strategy.sql`); `insightExists` SELECT-then-INSERT is TOCTOU | the daydream loop and the API handler can both insert the same pattern. Add `UNIQUE(scope, pattern_json)` and treat conflict as a no-op. |
| M17 | Digest `DaydreamSummary.Actions` is overwritten, not accumulated (`internal/digest/digest.go:155`) | with more than one JSON group, the Morning Digest reports the last group's count. Scan into a local and `+=`. |
| M18 | Doctor disk check runs before the state dir exists → false "could not determine free disk space" on fresh clone (`internal/doctor/doctor.go:142`) | reorder so the dir is created first, or fall back to the parent dir. |
| M19 | Scheduler `Ready` ignores `ready` while `Blocked` treats it schedulable (`internal/scheduler/schedule.go:39,59`) | latent stuck-task hazard. Make the two functions agree on the status's meaning. |
| M20 | DAG depth bound counts only dependency chains, not parent (phase) nesting (`internal/dag/validate.go:188`) | unbounded phase nesting passes any `MaxDepth`. Bound parent chains or document the scope. |
| M21 | DAG validator accepts duplicate dependency entries (`internal/dag/validate.go:83`) | a malformed graph is persisted. Reject duplicate keys. |
| M22 | `hitl.Repo.Create` doesn't validate severity in Go (unlike corrections) (`internal/hitl/hitl.go:130`) | an invalid severity surfaces as an opaque SQLite CHECK error. Add a `validSeverity` check. |
| M23 | `Await` records a wrong `resume_state` on a second call for an already-awaiting job (`internal/hitl/service.go:66`) | informational only (resume uses `awaiting_from`), but the payload is misleading in an audit. Reload `j` after transition. |

### Low

| # | Finding (location) | Note / suggested fix |
|---|---|---|
| L1 | Chunk ID truncated to 64 bits; a collision silently overwrites a chunk (`internal/mce/division/division.go:181`) | widen to 128-bit (as `compactionID` does) and/or verify `source_hash`/`byte_start` on conflict. |
| L2 | Incremental index skip keys on mtime+size, not content (`internal/mce/index.go:140`) | same-second / same-size edits are skipped and stale content served. Store a sampled fingerprint, or document the tradeoff. |
| L3 | Index model-call bound is per-file soft (`internal/mce/index.go:137`) | one large file can exceed `MaxChunks` by an order of magnitude. Pass a per-source budget. |
| L4 | Embedding dimension pin is a read-then-write outside a transaction (`internal/mce/embed.go:123`) | two first-embeddings can leave mixed dimensions. Enforce via a unique constraint. |
| L5 | Prompt `TotalToken` excludes the `\n\n` section joins (`internal/prompt/assemble.go:113`) | under-reports KV pressure by a handful of tokens. Compute from the joined text. |
| L6 | `run_tests`/`lint` interpolate the agent's `command` into `sh -c` (`cmd/athanor/pod_executor.go:102`) | contained by the pod, but blurs "task-defined test command" into arbitrary shell. Restrict to the task's configured command or document the equivalence. |
| L7 | base64 smuggling heuristic checks only the leftmost match (`internal/airlock/scanner/heuristic.go:138`) | `FindStringIndex` misses later long blobs. Use `FindAllStringIndex` and normalize whitespace. |
| L8 | Scanner `runCommand` treats exit 0 as Clean even with detection output (`cmd/athanor/scanners/scanners.go:93`) | comment says the opposite. Require empty stdout for Clean. |
| L9 | Re-fetch cap is a hardcoded 10 MiB, not `network.max_response_bytes` (`cmd/athanor/gateway_tool_adapter.go:215`) | thread the configured cap through. |
| L10 | `ExportPath` doesn't sanitize `projectID`/`artifactID` (`internal/airlock/egress/exports.go:27`) | currently unreachable (IDs resolve through the store first); defense-in-depth via `paths.Resolve`. |
| L11 | `validateSpec` doesn't reject a flag-like image name (`internal/jobpod/args_common.go:27`) | a name beginning `-` becomes a podman flag. Reject `-`/whitespace and pass `--`. |
| L12 | Gateway header sanitization is a 6-name denylist (`internal/gateway/sanitize.go:26`) | prefer an allowlist (build from an empty header map). |
| L13 | Auth middleware leaks job-ID existence via timing (`internal/internalapi/middleware.go:47`) | perform a dummy constant-time compare on a miss. |
| L14 | `events` table has no FK on `project_id`/`job_id` (`migrations/0001_execution_spine.sql:96`) | append-only with no deletes, so low impact; inconsistent with the FK discipline. |
| L15 | `migrate.currentVersion` swallows the query error → "version 0" (`internal/store/migrate.go:66`) | misleading failure message on an unhealthy DB. Propagate the error. |
| L16 | `store.Open` doesn't URL-escape the DSN path (`internal/store/store.go:28`) | a path with `?`/`#`/space breaks the DSN. Use URL-safe encoding. |
| L17 | Cron matcher ANDs day-of-month with day-of-week and rejects `7`=Sunday (`internal/backup/cron.go:54`) | diverges from Vixie cron semantics; only affects custom schedules. |
| L18 | Kill switch gates new work but doesn't tear down in-flight pods/LLM calls (`internal/control/killswitch.go:76`) | in-flight work runs to its phase boundary. Document, or cancel inference + pods on freeze. |
| L19 | `Freeze` returns an error even when the freeze succeeded (audit failure) (`internal/control/killswitch.go:82`) | fail-safe direction, but the error contract is misleading. |
| L20 | `Manager.Logger` accepts unknown categories when the configured set is empty (`internal/logging/logging.go:71`) | a typo'd category silently creates a new log file. Reject when a non-empty set is configured. |
| L21 | Rotation drops the log entry if rename fails (`internal/logging/rotate.go:44`) | lose one event on a transient FS error. Best-effort re-open. |
| L22 | Linux AC detection fails open when no `AC`/`ADP`-prefixed supply entry exists (`cmd/athanor/power_linux.go:33`) | treated as on-AC while on battery. |
| L23 | `doctor.modelPresent` is stricter than `onboard.baseName` (tag-stripping) (`internal/doctor/doctor.go:284`) | a base-name persona reads "missing" in doctor but "installed" in onboard. Align the tag-stripping. |
| L24 | `store.Backup` re-evaluates `time.Now()` inside the collision loop (`internal/store/backup.go:23`) | a second-and-later collision gets a fresh timestamp + suffix; sloppy but harmless single-daemon. |
| L25 | `battery_pause_threshold_percent: 0` is silently coerced to 20 (`internal/config/defaults.go:61`) | "never pause on battery" cannot be expressed. |
| L26 | Engine nil-guard inconsistency: `call()` derefs `e.registry` and `audit` derefs `e.db` without the guards `crossFamily`/`difficultyHint` use (`internal/engine/phases.go:166`, `engine.go:807`) | not reachable in production wiring, but the codebase advertises nil-safety unevenly. |
| L27 | `PassedTests` is misnamed (stores the overall pass flag, not "test command exited 0") (`internal/engine/evaluate.go:310`) | the name/comment invite future misuse; rename or correct the doc. |
| L28 | Divergence re-roll swallows `SetStatus` errors (`internal/engine/diverge.go:147`) | a failed discard can leave a below-floor candidate as `draft` and re-evaluate it. |
| L29 | `captureOutcome` is not idempotent (`internal/engine/strategy.go:122`) | an external re-Enqueue of a terminal job duplicates the outcome row (profiles/outcomes have `UNIQUE(job_id)`; `captureOutcome` doesn't use it). |
| L30 | Dead `reflecting → synthesizing` edge has no engine path (`internal/job/state.go:87`) | the comment describes a feature that doesn't exist; remove or implement. |

---

## Fixed (2026-10-16 review)

A hardening sweep driven by the same review. Each item is a separate commit
(`7bf9ca7`..`9c65b3b`); the titles carry the detail.

| Area | Fix |
|---|---|
| Scheduler · recovery (H8) | a blocked task (and its transitively blocked dependents) is now recoverable without manual DB edits: `Scheduler.Reset` (behind `POST /tasks/{id}/reset` / `athanor task reset`) returns them to `pending` and pins `retry_baseline` (migration 0026), so `handleFailure` grants a fresh §7.2 retry window instead of re-blocking on the next failure |
| Scheduler · atomicity (H7) | `advance` now creates the queued job and marks its task `running` in one transaction (`job.Repository.CreateTx` + `project.Repo.SetTaskStatusTx`), so a failed status write rolls back the job insert and can no longer leave an orphan that the next pass duplicates |
| Observability · bounded scans (M9) | `store.EventFilter` gained `AfterID` (an `id > ?` delta) and `store.LatestEventID`; the dashboard/watch SSE loops now fetch only new events in 500-row batches, and the egress poll drains the job-event log in 500-row batches instead of re-scanning the whole append-only table every 500 ms / 5 s |
| Engine · acceptance | the comparison verifier now runs on the **full** hash-verified artifact, not the 4 KB judge head (was falsely rejecting any code artifact >4 KB) |
| Engine · state machine | added the missing `paused → comparing` resume edge |
| Engine · reflection | the reflection proposal is now persisted and injected into the next divergence pass, and `max_reflection_loops=N` yields N full retries (was N reflections but N−1 re-divergences) |
| Engine · I/O | research fetches are bounded by the diverging-phase budget; `llm.Stream` fails loud on a truncated/aborted stream and an ollama `error` field |
| Alarms | a routine network denial no longer freezes the daemon (security → alert; only self-modification/drift stay critical); the detector matches genuine signals, not free-text `%denied%` |
| UI | the page title is escaped template data (was an XSS + template-injection vector via project names) |
| MCE | `Owns` no longer lets a job `context_swap` into another job's chunk |
| Power | `require_ac_for_deep_work` is now honored (was a dead knob) |
| Scheduler | a stale terminal callback for a superseded job is ignored (was double-counting retries) |
| Store | row + audit-event writes are atomic across `job.Create`, `project.SubmitGoal`, artifact create/status/git-commit; `SetTaskStatus` gained a CAS guard; the evaluation audit event uses `json.Marshal`; indexes on `events(project_id)` and `artifacts(job_id, kind)` (migration 0025) |
| HITL | task-escalation requests carry a TTL; boot reconcile re-applies a decided-but-unapplied request's outcome |
| Strategy | `Mine` dedups cohort membership (a repeated operation no longer double-counts) and gates the candidate-count axis on the divergence phase |
| Daydream | feedback review respects muted/expired global corrections |
| Gate | the structural gates now also forbid `os.StartProcess`, `golang.org/x/sys/unix`, and raw `net.Dial/Dialer/Listen` outside the gateway and loopback sites |

## Fixed (2026-10-07)

| Finding | Resolution |
|---|---|
| O1: a gateway policy denial (403) was indistinguishable from a tool-envelope violation | a policy denial is now `451` (`internalapi.ErrFetchDenied`) → `toolenvelope.ErrPolicyDenied` in the runner; the engine audits `outcome=denied` distinctly. Contract in [ADR-0019](adr/0019-gateway-tools.md) §1/§7; tests in `internalapi/gateway_tools_test.go`, `runner/httpclient_test.go`, `engine/research_test.go`. |
| O2: `GET /internal/v1/jobs/{id}` reported a hardcoded `State: "running"` | `handleJobGet` now returns the job's real state via `Repo.JobTask` (which also fixes F-3: job → task resolution). Tests in `internalapi/handlers_test.go`. |

## Fixed (2026-10-06 review)

| Finding | Resolution |
|---|---|
| Job Pods were created `--name <uuid>` but `Sweep` filtered `name=athanor-job-`, so crash orphans were never swept | `--name athanor-job-<uuid>` (`d0d884a`) |
| `Stop` removed the container but never cancelled `supervise`, leaking a goroutine + a failing `podman inspect` every 2 s | per-entry `context.CancelFunc`, cancelled in `Stop` (`d0d884a`) |
| `Start`'s duplicate-ID check and map insert were not atomic | reserve the ID under the lock, release on failure (`d0d884a`) |
| `airlock/paths.Validate` checked symlink escape only when the **final** component was a symlink; a symlinked directory under root escaped | always `EvalSymlinks` and require containment in the canonical root (`5052ecf`) |
| `logging` rotation left `w.f == nil` on a rename/reopen error → permanent write failure | reopen the original (or the rotated file) on failure (`95d6ae4`) |
| `gateway` per-host rate-limiter map grew without bound | idle + LRU eviction with a 1024-bucket cap (`95d6ae4`) |
| `backup` re-fired a failed scheduled job every 30 s within the same minute | mark the attempt before running (`95d6ae4`) |
| Engine failure path transitioned to `failed` without clearing the §10.5 suppression row | `clearSuppressedTiers` on the failure path (`155cc02`) |
| Probe config: an out-of-matrix `-judge-model` inherited the generator's family, silently satisfying the cross-family guard | fall back to the declared family or empty, never the generator's (`155cc02`) |

## Checked and clean

Gate G1 containment (no `os/exec` in `internal/`; `syscall` only in the
allowlisted `airlock/paths` build-tag wrappers), per-job token auth (CSPRNG,
constant-time compare, URL binding), the job state machine (legal-edge table +
CAS transition + atomic audit), the gateway (allowlist→resolver ordering,
`.`-anchored suffix match, deny-list precedence, private-IP guard, pinned-IP
dial, per-hop re-evaluation, size cap), the server (Host allowlist, cross-site
guard, auth-wrapped internal routes), tool-envelope enforcement on every
handler, and row/body/token-dir cleanup.

Verified clean in the 2026-10-16 review (no findings):
- **No SQL injection** — every dynamic query binds values with `?`; the one
  interpolated statement (`VACUUM INTO`) correctly quote-escapes.
- **Migration runner** — idempotent, forward-only, `foreign_key_check`-gated,
  backup-before-migrate, embedded `embed.go` correct.
- **FTS5 external-content setup** (migration 0013) — delete/insert trigger
  shape and `content_rowid` mapping are correct.
- **Single-connection discipline** — structurally enforced (`gate_tx_test.go`)
  and runtime-guarded; `MaxOpenConns(1)`.
- **MCE byte-exactness (P1–P5)** — division, `Reassemble`/`concat` (with
  contiguity + per-chunk hash), and `context_swap` all preserve bytes.
- **MCE KV-cache monitor** — strict-`>` 85%/95% thresholds, `maxContext<=0`
  and `activeTokens>=maxContext` floor-breach guards (no div-by-zero).
- **§10.5 assembly ladder** — eviction is 7→6→5→4 only; tiers 1–3 structurally
  absent from the ladder.
- **Compaction determinism** — content-addressed memo keyed on kind/template/
  persona/temperature/profile/hash; Temp-0.0 is a DB CHECK + config + adapter
  triple invariant.
- **Prompt assembly determinism** — fixed construction order; map iteration
  used only for pricing, never output ordering.
- **DAG cycle/reference validation** — Kahn's algorithm, parent/dependency
  resolution, acyclicity, coverage, and budget checks are deterministic.
- **Corrections §18.1/§18.4** — all nine sources mapped; mandatory form
  enforced; injection ordering severity→scope→recency correct.
- **Strategy profile/outcome idempotency** — `UNIQUE(job_id)` + `ON CONFLICT
  DO NOTHING`.
- **Auth constant-time compare**, **CSRF/Host-allowlist middleware**, and
  **loopback enforcement** — correct as reviewed.
