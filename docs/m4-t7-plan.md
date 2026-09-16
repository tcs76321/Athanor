# M4-T7 Plan — Gateway-backed tools (`fetch_url`, `search_web`)

**ADR:** [0019-gateway-tools.md](adr/0019-gateway-tools.md) ·
**Acceptance (ROADMAP §4):** "Tools unreachable except via gateway;
allowlist enforcement proven by test" (§25). Gate G4's research-goal
half is this task's demo.

## Commit sequence

Each commit: stage → `make check` → staged diff → one-line
`M4-T7.x:` message → human signs.

### T7.1 — ADR-0019 + this plan (docs)
Lock the seven decisions (route shape, `ToolGateway` inversion, redirect
hop loop, search template, envelope-widening policy, truncated re-fetch,
engine research sub-step) before any code.

### T7.2 — Widen the closed tool set
- `internal/toolenvelope/allowlist.go`: `ToolFetchURL`, `ToolSearchWeb`
  in the constants + `isKnown`; provenance comment updated.
- `allowlist_test.go`: closed-set enumeration updated.
- Config: `network.search_engine_url_template` field (default empty,
  validated: must parse as `text/template` producing an http(s) URL),
  `config.example.yaml` entry with dormant-flag wording, example-
  matches-defaults test updated.
- `internal/gate/gate_g2_test.go`: `toolNames` slice grows to the two
  new names; a route-existence assertion for the two new routes joins
  `TestGateG2ToolEnvelopeBypassImpossible`.

### T7.3 — Internal API routes + runner methods
- `internal/internalapi/gateway_tools.go` (new file, same package):
  request/response types, `handleFetchURL`, `handleSearchWeb`,
  `ToolGateway` interface, registration in `handlers.go` `Register`
  (middleware-wrapped — the Gate G2 count check scales).
- Error mapping: 403 `ErrToolDisallowed`; 400 bad input (unparseable
  URL, empty query); 501 `ErrSearchNotConfigured`; 502 fetch failure;
  422 reader-rejected.
- `internal/internalapi/runner/httpclient.go`: `FetchURL` / `SearchWeb`
  methods (typed bodies, same `post`-style helper).
- Tests mirroring `exec_test.go`: envelope 403, audit events, happy
  path with a fake `ToolGateway`.

### T7.4 — Redirect hop loop in the gateway + cmd adapter
- `internal/gateway/client.go`: bounded hop loop (max 5),
  `CheckRedirect: http.ErrUseLastResponse`, per-hop `Policy.Eval` +
  rate limit + pinned transport + audit event; `redirect_chain` on the
  final event.
- `internal/gateway/client_test.go`: hop-loop tests (allowlisted →
  allowlisted follows; any hop off-list/private/IDN/deny-list aborts;
  hop cap exhausted returns the last response with a flag).
- `cmd/athanor/gateway_tool_adapter.go`: `internalapi.ToolGateway` over
  `GatewayParts` (fetch_url = `Client.Fetch` + `Reader.Extract` +
  §6 re-fetch; search_web = template → `Client.Fetch` → `Reader.Extract`
  → link extraction); `serve.go` wiring (constructor grows — the
  M2-T4 precedent).
- Link extraction lives in `internal/gateway` (package doc keeps the
  single-egress-surface story).

### T7.5 — Engine research sub-step
- `internal/prompt`: planning schema + prompt text gain optional
  `sources: []`; assembly tests.
- `internal/engine`: `researchFetch` sub-step (pod_wiring.go pattern)
  for text/document archetypes with sources; soft-fail denials;
  `research_fetch` audit events; per-source budget; prompt injection
  of attributed markdown blocks.
- Tests: fake runner (fetched / denied / disallowed / error rows),
  prompt assembly with and without sources.

### T7.6 — Demo + close-out (docs)
- `docs/demo-m4-t7.md`: research goal e2e (allowlisted domains only)
  — Gate G4 evidence part 1. Config used: `default_policy: deny`,
  `allow_list: [the demo domains]`, task override granting
  `fetch_url`.
- README ("What's next" / core-characteristics bullet), CHANGELOG,
  ROADMAP status row (T7 done, T8 pending), ADR-0019 amendment if
  reality disagreed.
- Gates G1 (rule 6 walk) + G2 (envelope iteration + middleware count)
  re-proven in `make test-race`.

## Risks / notes

- `internalapi` must not import `gateway` (ADR-0019 §2): the adapter
  lives in `cmd/athanor/`.
- No new dependencies; no migrations (`network` event-name set is open).
- Gate G1 rule 6: all outbound HTTP stays inside `internal/gateway/`
  (the adapter calls the gateway, never `http` directly).
- The search template is validated at config load (parse + scheme +
  exactly one `{{.Query}}` action) so an operator typo fails at boot,
  not at first search.
