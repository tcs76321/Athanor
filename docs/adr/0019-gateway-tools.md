# ADR 0019 — Gateway-backed tools: `fetch_url` and `search_web` (M4-T7)

**Status:** Accepted · **Date:** 2026-09-15 · **Refs:** ARCHITECTURE §21.5,
§16, §25; ROADMAP M4-T7; ADR-0017 (gateway), ADR-0018 (Reader Mode),
ADR-0009 (engine pod wiring), ADR-0008 (per-job bearer tokens)

## Context

M4-T5 and M4-T6 built the §21.5 door — `gateway.Client` (allowlist,
DNS-rebinding guard, rate limit, header sanitization, size cap, `network`
audit) and `gateway.Reader` (readability → sanitize → markdown → injection
scan). The daemon constructs both at boot (`cmd/athanor/gateway.go`,
`GatewayParts{Client, Reader}`), and the construction comment says plainly:
*"T7's fetch_url / search_web tools consume both; until then the
construction is the structural proof."*

No tool consumer exists yet. The closed tool envelope
(`internal/toolenvelope`) is `{execute_code, run_tests, lint,
git_operation}`; the §25 table lists `fetch_url(url)` and
`search_web(query)` as tools with HITL "No (if domain allowlisted)". Job
Pods run with `--network=none` (M2), so a pod-side fetch is impossible by
design — the *Core* must execute the fetch, which is exactly why the route
must be envelope-gated: a job with `fetch_url` in its envelope is precisely
the "allowlisted internet" capability, granted per task, audited per call.

M4-T7 wires the tools. This ADR locks the architecture before code grows.

## Decision

### 1. The tools are internal API routes; the Core executes the fetch

Two new routes behind the existing auth middleware, mirroring the M3-T2
`lint` handler shape exactly:

```
POST /internal/v1/jobs/{id}/fetch_url    {url, timeout_seconds}
POST /internal/v1/jobs/{id}/search_web   {query, timeout_seconds}
```

Handler flow (identical in shape to `handleExecuteCode` /
`handleRunTests` / `handleLint`): parse body → per-job envelope check via
`a.tools.EnvelopeFor` (403 + `toolenvelope.ErrToolDisallowed` when the
tool is not in the envelope) → `auditAllow` → dispatch → typed error
mapping. The dispatch target is the §21.5 gateway, reached through a
narrow interface (§2 below). Callers are the engine (via the
`internal/internalapi/runner` loopback client, exactly as a Job Pod would
call) and, in the future, pod-side code through the same routes.

Response shapes:

- `fetch_url` → `{status_code, url, title, site_name, excerpt, markdown,
  mode (readability|plain|raw), truncated, content_type}` — `markdown` is
  empty and `mode` is `raw` when Reader Mode is disabled or the content
  is not extractable; the raw bytes are never returned inline (they live
  in the gateway's own audit trail) because a tool response is prompt
  material, and prompt material must have passed the injection scan.
  A non-2xx upstream status is returned as a successful tool call with
  `status_code` set — the caller decides whether an HTTP 404 is
  task-relevant. Transport errors and policy refusals are tool-call
  errors.
- `search_web` → `{results: [{title, url, snippet}], engine}` — parsed
  from the search engine's results page (§4).

### 2. Dependency inversion: `internalapi` owns a `ToolGateway` interface

`internal/internalapi` gains:

```go
type ToolGateway interface {
    FetchURL(ctx context.Context, req FetchURLRequest) (*FetchURLResponse, error)
    SearchWeb(ctx context.Context, req SearchWebRequest) (*SearchWebResponse, error)
}
```

satisfied by an adapter in `cmd/athanor/` (`gateway_tool_adapter.go`) over
`GatewayParts{Client, Reader}` — the same inversion pattern as
`TokenStore` (ADR-0008) and `ToolEnvLookup` (M2-T4). `internalapi` does
not import `internal/gateway` at all: the adapter in `cmd/` holds both
types, so the internal API package stays gateway-agnostic and tests inject
a fake. No import cycle exists in any placement (`gateway` imports only
`store` + `airlock/scanner`), but the interface keeps the §25 tool surface
describable without dragging the whole gateway into the internal API's
public shape.

### 3. Redirects: bounded hop loop inside `Client.Fetch`, policy re-evaluated per hop

**The gap this closes.** `Client.Fetch` sets no `CheckRedirect`;
`http.Client` follows up to 10 redirects by default, and `Policy.Eval`
ran only on the first URL. An allowlisted host that 302s to
`http://127.0.0.1:9200` (or to any off-list host) would be fetched —
the allowlist would be bypassed by a redirect. Worse, the per-request
transport pins the *first* hop's resolved IPs into `DialContext`; a
redirect to a different host would be dialed against the wrong IP set
entirely.

**Decision.** Redirect following moves inside `Client.Fetch` as a bounded
hop loop (max 5 hops, a package constant, not config). Each hop:

1. runs `Policy.Eval` on the hop URL (off-list, deny-list, IDN, and
   private-IP denials abort with the ordinary typed errors);
2. goes through the per-host rate limiter (the bucket of the hop's own
   host);
3. dials through a transport pinned to *that hop's* resolved IPs;
4. appends its own `network` audit event (`event=fetched`, with
   `hop` and, for the final hop, the accumulated `redirect_chain`).

The underlying `http.Client` is constructed with
`CheckRedirect: http.ErrUseLastResponse` so the standard library never
follows a redirect on its own; the loop is the only follower. A response
without a redirect status (or with an unparseable `Location`) is the
final response. This keeps every containment property (allowlist,
rebinding guard, rate limit, size cap, audit) true of *every* byte that
enters, not just the first hop's.

### 4. `search_web` is a configured engine template, not a built-in search backend

No search-engine integration exists and none is added as a dependency.
`network.search_engine_url_template` (config field, default **empty**)
is a `text/template` URL with `{{.Query}}` (stdlib; a bare `%s` `fmt`
placeholder is rejected because it cannot be validated without
formatting side effects). Example:

```yaml
network:
  search_engine_url_template: "https://html.duckduckgo.com/html/?q={{.Query}}"
```

Semantics:

- Unset → `search_web` returns the typed error `ErrSearchNotConfigured`
  (HTTP 501 from the route). The tool is registered in the envelope but
  inert until the operator configures an engine — the dormant-flag
  pattern (`f309500`) applied to a tool.
- The template host is **not** special-cased: the built URL goes through
  the ordinary `Policy.Eval`, so an engine not in `allow_list` is denied
  like any other host. The template is a convenience for constructing
  the query URL, never an exemption from the allowlist.
- The query is URL-escaped before templating; a query that would inject
  additional URL syntax is inert because the escaping happens first.
- The results page is fetched through the gateway (policy, rate limit,
  size cap, audit — all ordinary) and passed through the Reader
  pipeline. A light in-package link extraction over the sanitized tree
  produces `{title, url, snippet}` items; if extraction finds no result
  links, the tool returns an empty `results` array (the engine's markup
  changed — an empirical fact the operator sees, not a hidden failure).
  Result URLs in the response are *not* automatically fetched; fetching
  them is a subsequent `fetch_url` call, so every byte still enters
  through one audited door.

### 5. Envelope widening: per-task override only, not the daemon default

`toolenvelope` gains `ToolFetchURL = "fetch_url"` and `ToolSearchWeb =
"search_web"`. The closed set grows; **`config.job_pod.default_tools`
does not**. Jobs get the tools only via the per-task
`allowed_tools_json` override, so network access is an explicit task
decision, auditable in the task definition itself, and the Gate G4 demo
can show the override being granted. Gate G2 coverage follows the
closed-set iteration in `TestGateG2ToolEnvelopeBypassImpossible` plus a
route-existence assertion for the two new routes (the ADR-0009 pattern).

### 6. Truncated re-fetch: one retry at a halved cap, flag preserved

ADR-0018 deferred the `Response.Truncated` re-fetch decision to "the
caller (T7)". Decision: when `ReaderResult.Truncated` is set, the
`fetch_url` adapter retries **once** with a halved response cap (the
gateway's `Options.MaxResponseBytes` is per-Client, so the retry
constructs a short-lived client variant with the halved cap), takes
whichever result has more extracted markdown, and always surfaces
`truncated: true` in the tool response if either pass truncated. One
retry only — a page too big at cap/2 twice is a page the caller should
meet through a more specific URL, and the audit trail records both
attempts.

### 7. Engine call site: planner-emitted `sources`, fetched during a research sub-step

The M1 flow is plan → diverge → evaluate → compare; the agentic
tool-calling loop is M5+. The T7 call site is therefore engine-driven,
mirroring `pod_wiring.go`'s sub-step pattern (ADR-0009/0014):

- The planning prompt schema gains an optional `sources` array (URLs) —
  the §16 research workflow's "retrieve allowed sources" step made
  explicit in the plan instead of scraped from prose.
- For `text`/`document` archetypes with non-empty `sources`, the engine
  runs a `research` sub-step before divergence: one `runner.FetchURL`
  call per source (sequential, per-phase budget applies), extracted
  markdown injected into the diverge/synthesis prompt as attributed
  fenced blocks with a per-source character budget.
- Denials and `ErrToolDisallowed` **soft-fail** (audit row, source
  dropped, job continues) — the M1 precedent; a denied source is a
  containment event, not a job failure. Only transport-level errors on
  *every* source escalate.
- Every attempt appends a `research_fetch` audit event: `url`,
  `outcome` (`fetched` | `denied_off_list` | `denied_private_ip` |
  `disallowed` | `reader_rejected` | `error`), `mode`, `bytes`,
  `truncated`.

## Consequences

- The §25 tool surface grows by two tools, both Core-executed and
  envelope-gated. Adding them to the closed set is the project decision
  this ADR records (AGENTS.md §Dependencies discipline applied to the
  §25 surface).
- `Client.Fetch` gains the redirect loop; its failure modes are typed and
  audited per hop. The first-hop-only policy evaluation — a latent SSRF
  bypass — closes in T7, before the adversarial suite (T8) codifies it.
- `search_web` is inert without configuration. Operators see the tool in
  the envelope set but get `ErrSearchNotConfigured` until they set the
  template; the example config documents this with the dormant-flag
  wording.
- The engine's research sub-step is synchronous and sequential — a
  source list is short (the planner is budgeted), and parallel fetching
  would need rate-limiter fairness work no task has asked for. If a
  future probe shows fetch latency dominating the research phase, the
  sub-step can fan out behind the per-host buckets.
- No migrations: the `network` category's event-name set is open
  (ADR-0017 §8); `research_fetch` joins it. `jobs` category gains
  nothing new (`tool_call` / `tool_disallowed` already exist).

## Not in M4-T7

- `browser_mode` (§25): flag stays dormant; M6 owns the HITL runtime.
- Cloud-inference credential injection (§21.7): M6.
- Automatic fetching of search-result URLs: each fetch is an explicit
  `fetch_url` envelope-gated call (§4 above).
- Per-task linter/search-engine overrides beyond the single template:
  future config work, driven by operator need.
- Pod-initiated tool discovery/negotiation: pods call the routes the
  envelope admits; no capability negotiation exists in M4.

## Forward references

- **M4-T8** builds the adversarial suite on this: SSRF corpus (private
  IPs, rebinding, redirect escapes — the §3 loop is its primary
  subject), allowlist-bypass corpus (suffix, userinfo, trailing-dot,
  scheme attacks), injection corpus (Reader), and the
  `insecureSkipPrivateIPGuard` production-defaults regression test
  ADR-0017 §4 promised.
- **M5/M6** may replace the engine research sub-step with the agentic
  tool loop; the routes and envelope semantics do not change.

## Amendment (M4-T7.5, 2026-09-15) — §7 call-site pivot

**Reality over plan (ROADMAP §9.3).** §7 specified planner-emitted
`sources` fetched during a research sub-step. Implementation found the
M1 planner output is free text that `phasePlan` discards — there is no
structured plan schema to hang a `sources` array on, and building one
is M5-scale prompt-schema work.

**Amended decision.** The research sources are **task-declared URLs**:
the sub-step extracts absolute http(s) URLs from the task description
and the project goal (deduped, trailing prose punctuation stripped,
capped at 8), fetches exactly those through the envelope-gated
`fetch_url` route, and injects the extracted markdown into every
divergence candidate's prompt as attributed blocks (per-source
prompt-side budget 8000 chars). Nothing is scraped from LLM prose;
the human wrote the URLs. The per-task `fetch_url` envelope override
remains the capability gate — a task without it soft-fails every
fetch into a `research_fetch` audit row.

**Second simplification.** §7 said "only transport-level errors on
*every* source escalate." Amended to **soft-fail always**: research
context is an enhancement, and a job must not die because a
documentation site is down. Every attempt (fetched / denied /
disallowed / empty / error) is audited; a source-less divergence
proceeds exactly as before this task.

Crash-recovery note: a crash mid-divergence re-runs the phase, so the
fetches re-run too — idempotent downloads, gateway-audited and
rate-limited. No artifacts are persisted by the sub-step (sources are
inputs, not outputs).
