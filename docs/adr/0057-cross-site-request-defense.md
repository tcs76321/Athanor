# ADR 0057 — Cross-site request defense for the loopback HTTP surface

**Status:** Accepted — lands with F5 · **Date:** 2026-10-06 · **Refs:**
ARCHITECTURE §3.2, §21.8; [ADR-0011](0011-external-api-host-allowlist.md);
ROADMAP F5; `internal/server/cross_site.go`

## Context

The daemon binds loopback only (ARCHITECTURE §21.8) and its external HTTP
surface (the JSON API and the local web UI) shares one mux with the
Job-Pod internal API (`cmd/athanor/serve.go`). ADR-0011 added a Host-header
allowlist middleware that closes **DNS rebinding**: an attacker-controlled
hostname that resolves to `127.0.0.1` is rejected because its `Host` header
is not in the allowlist.

That defense does not cover **cross-site request forgery (CSRF)**. A page the
user visits can issue a request to `http://127.0.0.1:7420` whose `Host`
header is the legitimate `127.0.0.1:7420` — the allowlist passes it. A
"simple request" (no custom headers, no preflight) is delivered by every
browser. The surface has several such endpoints:

- `POST /freeze` takes no body and freezes the system.
- The UI's form handlers (`POST /ui/approvals/{id}`, `/ui/jobs/{id}/control`,
  `/ui/corrections/...`, `/ui/projects/{id}/corrections`) read
  `r.ParseForm()`/`FormValue`, i.e. ordinary form posts. Approving a pending
  `git_push` is among them.
- Bodyless JSON routes (`POST /strategy/mine`, `POST /exports/{id}`,
  `POST /projects/{id}/index`, `POST /alarms/{id}/resolve`).

Because the browser does not expose the response without CORS, an attacker
cannot read data, but the side effects are real and persistent. Modern
Chromium's Private Network Access and its preflight are partial and not
universal; the daemon cannot rely on the browser for this.

## Decision

A `crossSiteMiddleware` wraps the mux (inside the Host middleware) and
inspects **only state-changing methods** (`POST`, `PUT`, `PATCH`, `DELETE`).
A request is allowed when any of the following holds:

1. `Sec-Fetch-Site` is `same-origin` or `none` (the daemon's own pages, or a
   user-typed navigation).
2. Neither `Sec-Fetch-Site` nor `Origin` is present — a non-browser client
   (the CLI, a Job Pod, `curl`). Such a client has no ambient browser
   authority, and Job Pods are additionally bound by the bearer-token
   middleware (ADR-0008).
3. `Origin` is exactly one of the daemon's own origins.

Everything else is `403 Forbidden`.

The accepted-origin set is derived from the same host allowlist
(`network.external_api_host_allowlist`), in both `http://` and `https://`
forms, so there is a single source of truth and no new config key. Safe
methods (`GET`, `HEAD`, `OPTIONS`, `TRACE`) are never blocked.

## Consequences

- The CSRF class is closed for every state-changing route with no client
  changes: the UI's same-origin form posts and the CLI/pod requests all
  continue to work.
- The internal API shares the mux but its callers send neither header, so
  rule 2 applies and the guard is a no-op for pods.
- The empty-host-allowlist test escape hatch disables the Host check but not
  this guard; with no accepted origins a cross-site request is still refused.
- The guard is structural and header-based, so it is unit-testable without a
  browser (see `internal/server/cross_site_test.go`).

## Alternatives rejected

- **A per-session CSRF token cookie.** Stronger, but it requires cookies, a
  session, and rewriting every UI form/JS fetch; the proxy-immune
  `Sec-Fetch-Site`/`Origin` check needs none of that.
- **Requiring a custom header (e.g. `X-Athanor-Client`).** Browsers cannot
  send a custom header cross-origin without a preflight, but a plain HTML form
  cannot set one either, so the UI would need JS-only forms.
- **Requiring `Content-Type: application/json` on the JSON API.** Partial
  (it does not cover the bodyless routes) and it would break the UI's form
  posts if applied globally; kept as a possible future belt-and-suspenders.
- **Extending the Host allowlist to check `Origin`.** Conflates two headers
  and two attacks; kept separate deliberately.

## Not in scope

- Response-level hardening (CORS headers, CSP). The browser already cannot
  read the response; this ADR is about the side effect, not the data.
- Anything requiring remote (non-loopback) access; that is out of scope per
  §21.8.
