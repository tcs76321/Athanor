# Changelog

All notable changes to Athanor are recorded here. Format: one line per
`M#-T#: <title>` commit, in reverse chronological order (newest first),
with a backreference to the gate test, demo script, or probe that
proves the change. The "Prior work" section below summarizes the
commits before this changelog was initialized (F6).

New entries are appended at the top. Do not rewrite history.

## Unreleased

### M4 — Airlock & Gateway (in progress)

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


