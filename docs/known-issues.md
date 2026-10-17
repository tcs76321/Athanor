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

## Open

No findings are open as of 2026-10-16. The high/medium/low items from that
review are all resolved — most by code, a few by an explicit design decision or
a documented trade-off recorded below. New findings resume here.

---

## Fixed (2026-10-17 soak)

Two parser-robustness defects found by the 0.0.0 soak ladder (`granite3b`):

- **Duplicate `=== FILE ===` blocks failed the candidate.** A model that
  restated a file (`solution.py` twice) hit `toolenvelope: duplicate file path`
  and the whole evaluation failed. `ParseFileMarkers` now merges duplicates
  last-wins; stored manifests stay strict. (`internal/toolenvelope/files.go`,
  `TestParseFileMarkersDuplicateLastWins`.)
- **A truncated verdict failed the job.** A degenerate repetition loop generated
  a verdict until `max_output_tokens`, leaving `unterminated JSON object`. The
  parser now salvages a truncated object (bounded, audited as
  `json (truncated→salvaged)`); only an unrecoverable truncation errors.
  (`internal/engine/parse_verdict.go`, `TestParseVerdictJSON_TruncatedSalvaged`.)

---

## Fixed (2026-10-16 review)

A hardening sweep driven by the same review. Each item is a separate commit
(`7bf9ca7`..`9c65b3b`); the titles carry the detail.

| Area | Fix |
|---|---|
| Scheduler · recovery (H8) | a blocked task (and its transitively blocked dependents) is now recoverable without manual DB edits: `Scheduler.Reset` (behind `POST /tasks/{id}/reset` / `athanor task reset`) returns them to `pending` and pins `retry_baseline` (migration 0026), so `handleFailure` grants a fresh §7.2 retry window instead of re-blocking on the next failure |
| Scheduler · atomicity (H7) | `advance` now creates the queued job and marks its task `running` in one transaction (`job.Repository.CreateTx` + `project.Repo.SetTaskStatusTx`), so a failed status write rolls back the job insert and can no longer leave an orphan that the next pass duplicates |
| Observability · bounded scans (M9) | `store.EventFilter` gained `AfterID` (an `id > ?` delta) and `store.LatestEventID`; the dashboard/watch SSE loops now fetch only new events in 500-row batches, and the egress poll drains the job-event log in 500-row batches instead of re-scanning the whole append-only table every 500 ms / 5 s |
| Gateway · Reader scanners (M3) | the Reader Mode pipeline now runs a **scanner set**, not the regex heuristic alone: `gateway.ReaderOptions.Scanners` takes YARA (with the resolved in-tree rule set) and ClamAV adapters when their binaries are installed, aggregated worst-wins with the heuristic, so fetched web content is scanned by the same pluggable contract as ingress (`cmd/athanor/gateway.go:newReaderScanners`) |
| Gate G2 (M4, M5) | the route-auth proof now parses the registration AST (catching `mux.HandleFunc` / sub-muxes, and forbidding them), and the envelope-bypass proof derives its tool set from the new `toolenvelope.AllTools()` closed-set exporter, failing on any tool that is neither handled nor explicitly unrouted |
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

## Fixed (2026-10-16 medium/low sweep)

Every medium and low finding from the 2026-10-16 review, resolved. Grouped by
subsystem; the CHANGELOG section titles carry the detail and each change has a
regression test.

| Findings | Resolution |
|---|---|
| M1 | multi-owner chunk attribution (`context_chunk_owners`, migration 0027) so identical content in two scopes is indexed for both |
| M2 | `validateRaw` range-checks every bounded numeric config field; a negative is rejected loudly |
| M6 | corrected the `OpenNoFollow` doc: the guarantee is final-component only (`openat2`/`RESOLVE_BENEATH` would close the intermediate race) |
| M7 | quorum votes are decorrelated by folding the vote index into the derived judgment seed |
| M8 | documented that `judge_count` is authoritative and quorum applies to comparison (the evaluation judge is a single advisory call) |
| M10 | `lineDiff` is Hirschberg (linear space) with a `maxDiffLines` cap |
| M11 | `CreateDraft`/`NewVersion` remove the content file on any rollback |
| M12 | unknown battery level fails closed (pause); `require_ac_for_deep_work: false` still permits battery work |
| M13 | baked `deploy/core-config.yaml` into the Core Pod image (`host.containers.internal`) |
| M14 | `Mine` uses a leave-one-cohort-out baseline |
| M15 | `Backfill` derives the real project archetype (no synthetic `legacy` bucket) |
| M16 | `UNIQUE(scope, pattern_json)` (migration 0028) + upsert; a retired insight re-emerges |
| M17 | Morning Digest sums the daydream action count across groups |
| M18 | doctor's disk check falls back to an existing ancestor directory |
| M19 | `Ready` and `Blocked` both treat `ready` as schedulable |
| M20 | the DAG validator bounds parent (phase) nesting, not only dependency chains |
| M21 | duplicate `DependsOn` entries are rejected (`KindDuplicateRef`) |
| M22 | `hitl.Repo.Create` validates severity in Go |
| M23 | `Await` records `awaiting_from` as the resume state on repeated calls |
| L1 | chunk ids widened to 128 bits |
| L2 | incremental-index skip keys on nanosecond mtime |
| L3 | per-file model-call budget in `RunOnce`/`Enrich`/`embedSource` |
| L4 | embedding dimension pin read+write in one transaction |
| L5 | prompt `TotalToken` counts the section joins |
| L6 | documented the `run_tests`/`lint` shell containment (task-defined command, pod boundary) |
| L7 | the base64 heuristic scans every run |
| L8 | `scanners.runCommand` requires empty stdout for a clean verdict |
| L9 | the re-fetch cap threads `network.max_response_bytes` |
| L10 | `egress.ExportPath` validates components through `paths.Resolve` |
| L11 | flag-like / whitespace image names rejected; `--` precedes the image |
| L12 | gateway request headers are an allow-list (was a 6-name deny-list) |
| L13 | the auth middleware performs a dummy constant-time compare on a token miss |
| L14 | resolved by design: the audit columns are intentionally not foreign keys (the log outlives pruned entities); documented |
| L15 | `currentVersion` propagates its query error through `Migrate` |
| L16 | `store.Open` percent-escapes the DSN path |
| L17 | cron accepts `7` as Sunday and ORs dom/dow when both are restricted |
| L18 | resolved by documentation: freeze gates new work; in-flight work runs to its phase boundary |
| L19 | `Freeze`/`Unfreeze` report success once persisted (a failed audit is logged) |
| L20 | unknown log categories are rejected against the closed §28.1 set |
| L21 | rotation reopens the original and still writes the entry on a rename failure |
| L22 | Linux AC detection fails closed when no AC/ADP supply entry exists |
| L23 | `doctor.modelPresent` matches a bare base name against any tag |
| L24 | `store.Backup` captures the timestamp once |
| L25 | resolved by documentation: `battery_pause_threshold_percent: 0` is "unset"; use `require_ac_for_deep_work: false` to never pause |
| L26 | `call` guards a nil registry; `auditCat` guards a nil store |
| L27 | corrected the `PassedTests` doc (overall pass flag, not the test exit alone) |
| L28 | the divergence re-roll audits failed discards |
| L29 | verified already fixed: `strategy.Repo.CreateOutcome` is idempotent (`ON CONFLICT(job_id) DO NOTHING`) |
| L30 | removed the dead `reflecting → synthesizing` edge |

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
