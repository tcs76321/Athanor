# M4-T7 — Gateway-backed tools: `fetch_url` / `search_web` (Demo Script)

This is the executable proof for the M4-T7 tool surface (ADR-0019):
`fetch_url` and `search_web` are reachable **only** through the
envelope-gated internal API routes, the Core executes every fetch
through the §21.5 gateway (policy → rate limit → redirect hop loop →
size cap → Reader), and every attempt leaves an audit row. The script
is unit-test based by default (no internet required); the optional
daemon walk needs one allowlisted domain.

Commits: T7.1 (ADR-0019 + plan), T7.2 (closed-set widening +
`search_engine_url_template`), T7.3 (routes + runner + Gate G2),
T7.4 (redirect hop loop + adapter), T7.5 (engine research sub-step +
ADR amendment), T7.6 (this close-out).

## What M4-T7 proves

| Property | Proof |
|---|---|
| Tools are unreachable except via the gateway | `internalapi.ToolGateway` is the only dispatch surface; the adapter calls `gateway.Client`/`Reader`, and Gate G1 rule 6 keeps all outbound HTTP inside `internal/gateway/` |
| Allowlist enforcement proven by test | `TestFetch_DeniesOffListRedirect` + the whole `internal/gateway` policy corpus; `TestFetchURL_RejectsWhenToolNotInEnvelope` (403 + `tool_disallowed` audit row) |
| Redirect-SSRF gap closed (ADR-0019 §3) | `TestFetch_DeniesOffListRedirect`, `TestFetch_RedirectLoopHitsCap`, `TestRedirectTarget_Table` (10 rows), `TestFetch_TemporaryRedirectNotFollowed` |
| Envelope gate is server-side and structural | Gate G2: `TestGateG2ToolEnvelopeBypassImpossible` (closed set now includes both tools) + `TestGateG2GatewayToolRoutesRegistered` + the middleware-count check |
| Raw bytes never reach a prompt | `TestFetchURL_NoRawBytesEver`; injection-flagged content → `ErrContentRejected` → HTTP 422 (`TestFetchURL_InjectionFailsClosed`) |
| `search_web` inert until configured; template host not exempt | `TestSearchWeb_NotConfigured` (501), `TestSearchWeb_HappyPathEscapesQuery`, config-load validation (`TestNetwork_SearchTemplateValid/Invalid`) |
| Truncated re-fetch (ADR-0019 §6) | `TestFetchURL_TruncatedRefetchChoosesBetter` (one retry at cap/2, flag preserved) |
| Research sub-step is task-declared and soft-fail | `TestResearchContext_*` (4 tests) + `TestDiverge_InjectsResearchIntoCandidates` |
| Every attempt is audited | `tool_call` / `tool_disallowed` (`jobs`), `network` events per fetch hop, `research_fetch` / `research_start` (jobs) |

## Prerequisites

- Go 1.26+ with CGO (see [DEVELOPMENT.md](../DEVELOPMENT.md))
- `make check` (lint + vet + race tests)

## The structural proof (≈ 5 seconds)

```bash
make check
```

Expected (truncated):

```
ok  github.com/tcs76321/athanor/internal/gateway
ok  github.com/tcs76321/athanor/internal/internalapi
ok  github.com/tcs76321/athanor/internal/engine
ok  github.com/tcs76321/athanor/internal/gate
```

The `internal/gate` line re-proves Gate G1 (rule 6 walk — the new
adapter builds fetch requests with `gateway.NewRequest`, never raw
`http.`) and the Gate G2 structural tests (route middleware wraps,
envelope-bypass impossibility, argv hardening).

## The behavioral proof (unit-level, no internet)

```bash
CGO_ENABLED=1 go test ./internal/internalapi/ -run 'FetchURL|SearchWeb' -v
CGO_ENABLED=1 go test ./internal/gateway/    -run 'Redirect|Temporary' -v
CGO_ENABLED=1 go test ./internal/engine/     -run 'Research|Diverge' -v
```

The first line walks the route contract: 401 without a bearer token,
403 + `tool_disallowed` when the per-task envelope lacks the tool,
400 for malformed input, 501 when `search_web` is unconfigured,
503 when no gateway adapter is wired, 200 with the extracted
markdown on the happy path. The second line proves the redirect
hop loop (off-list redirect denied *before* the dial; loop cap;
307 unfollowed). The third proves the engine research sub-step
(soft-fail matrix, attribution, divergence injection).

## The daemon walk (optional; needs one allowlisted domain)

1. Allowlist a domain and grant the tool to a task:

   ```yaml
   # config.yaml
   network:
     default_policy: deny
     allow_list: ["docs.yourdomain.test"]
   ```

   Then create a task whose description cites
   `https://docs.yourdomain.test/research` and whose
   `allowed_tools_json` override includes `fetch_url`.

2. Start the daemon (`make run`) and submit the project. Watch the
   event log:

   ```bash
   athanor job watch -job <id>
   ```

   Expected rows: `research_start` (sources=N) → one `research_fetch`
   per URL (`outcome=fetched`, `mode=readability`) → the ordinary
   divergence/evaluation/comparison flow with the extracted markdown
   inside every candidate's prompt.

3. Deny-path spot-check: change the task's URL to a domain outside
   the allowlist. Expected: `research_fetch` with
   `outcome=disallowed` (or the gateway's `network`
   `allowlist_decision=denied_off_list`), the job continues, and the
   divergence runs without the source.

## Gate G4 progress

- [x] Tools unreachable except via gateway; allowlist enforcement
      proven by test (this demo, unit level; T8 adds the adversarial
      corpus and the opt-in behavioral probe).
- [ ] Adversarial suite green (M4-T8).
- [x] Research goal completes end-to-end using only allowlisted
      domains (`TestDiverge_InjectsResearchIntoCandidates` is the
      automated form; step 2 above is the daemon walkthrough).
