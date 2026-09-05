# ADR 0017 — Internet Gated Reader (Gateway) architecture (M4-T5)

**Status:** Accepted · **Date:** 2026-09-05 · **Refs:** ARCHITECTURE §21.5, §21.6, §21.7; ROADMAP M4-T5; ADR-0011 (external API Host-header allowlist), ADR-0015 (airlock pipeline / scanner interface), ADR-0008 (per-job bearer tokens)

## Context

M4 (Airlock & Gateway) is the milestone that unlocks real file and
network capability for the agent. T1–T4 (`5f829b7`, `0f727b0`,
`28e53cc`, `3b41b89`) shipped the file-airlock half: path
containment, ingress, pluggable scanners, and egress. The Gateway
is the network half — the **only** door between Athanor and the
public internet.

Today the door does not exist. The Core has no HTTP client to the
outside (zero hits for `net/http` client usage in `internal/`). Job
Pods run with `--network=none` (M2). The §21.5 responsibilities —
domain allowlist, rate limiting, request logging, header
sanitization, response size caps, Reader Mode extraction (T6), and
cloud-inference mediation (M6) — are documented but unimplemented.

M4-T5 builds the Gateway per §21.5 responsibilities 1–5. T6
adds Reader Mode extraction. T7 wires the gateway-backed tools.
M4-T8 is the adversarial security suite. M6 owns §21.7 cloud
mediation. This ADR locks the *architecture* of the gateway before
any code grows; the implementation lands across three commits
(T5.1 ADR + skeleton, T5.2 behavior, T5.3 close-out).

## Decision

### 1. Location: `internal/gateway/` (new package)

The gateway is its own package. Two reasons:

- **Single dispatch point for outbound HTTP.** The §21 containment
  story is "the agent reaches the internet through the gateway,
  nowhere else." That story is only durable if there is exactly
  one package whose job is outbound HTTP. A scatter of `http.Get`
  calls across `internal/` is structurally unsearchable; a single
  package is one structural gate rule.
- **Dependency inversion.** Every consumer of the gateway takes a
  `gateway.Client` interface, not the concrete type. Tests inject
  a fake; production wires the real `http.Client` with the
  resolved-IP-pinned `Transport`. The fake is also the seam
  through which T7's `fetch_url` / `search_web` tools (and M6's
  cloud-inference mediation) plug in.

### 2. Gate G1 rule 6: outbound HTTP lives only in `internal/gateway/`

The existing `internal/gate/gate_test.go` walks every `internal/`
and `cmd/` source file's imports. Rule 6 extends the walk to
*function calls*: every reference to `http.Get`, `http.Post`,
`http.NewRequest`, `http.DefaultClient.Do`, or
`(*http.Client).Do` outside `internal/gateway/` is a violation.

The walk is the same shape as the existing `syscall` identifier
check (rule 5): a closed allowlist of function names, an AST
inspector that finds every `SelectorExpr` whose `X` is `http` or a
`*http.Client`, and a check that the file lives under
`internal/gateway/`. The transport is permitted elsewhere
(`internal/server/server.go` already uses `http.Server` for the
loopback daemon) — rule 6 is specifically about *outbound client*
usage, not inbound server usage. The structural pattern: only
`internal/gateway/` may use `http.Client.Do` /
`http.NewRequestWithContext` for outbound calls; everywhere else
that needs HTTP uses the daemon's loopback server through the
internal API.

The rule lives as a separate test function
(`TestGateG1NoOutboundHTTPOutsideGateway`) in `gate_test.go`, not
folded into the existing `TestGateG1NoToolExecution`, because the
walk targets a different AST shape (call sites, not imports). The
existing test stays readable; the new test gets its own name and
its own violation counter.

### 3. Allowlist semantics: suffix-match with explicit `.` separator

The naive implementation `strings.HasSuffix(host, allow)` is
**unsafe**: an allowlist of `wikipedia.org` matches
`evilwikipedia.org`. The fix is the standard one:

```
match(host, allow) := host == allow
                   ||  strings.HasSuffix(host, "." + allow)
```

Both the host and the allowlist entry are lowercased to ASCII
before comparison. Non-ASCII hostnames are punycoded via
`golang.org/x/net/idn` — *wait, no new deps for T5* — so the
constraint is that the *allowlist* is ASCII-only and the *host* is
ASCII-only. IDN is a T6-or-later concern; the current operator
interface is `example.org`, not `例え.jp`. The ADR records this
as a known limitation: an IDN hostname in a URL the gateway
receives is rejected with `ErrIDNNotSupported` until the idn
package is added deliberately. The fix is small (~5 lines), the
review is not.

A `deny_list` field exists for explicit overrides. Use is a smell:
if an operator needs to allow `example.org` and block
`evil.example.org`, the right answer is "do not allow
`example.org` in the first place — list the specific subdomains you
need." The field exists because the alternative is a hard reject
of any non-trivial allowlist, and the practical reality is that
operators will widen the allowlist instead. The smell is
documented; the field ships.

### 4. DNS-rebinding prevention: refuse private/loopback/link-local resolutions

Before the gateway connects, it resolves the target hostname's
IPs and refuses the request if **any** resolved IP is in:

- RFC 1918: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
- Loopback: `127.0.0.0/8`, `::1/128`
- Link-local: `169.254.0.0/16`, `fe80::/10`
- Carrier-grade NAT: `100.64.0.0/10`
- Multicast: `224.0.0.0/4`, `ff00::/8`
- Unspecified: `0.0.0.0`, `::`
- Documentation/reserved ranges (RFC 5735, RFC 6890)

This is the §21.5 SSRF defense. A malicious allowlisted domain
that resolves to `127.0.0.1` (e.g. via a DNS-rebinding PoC or a
local service claiming a public name) is refused at resolve time,
before the TCP connect.

The resolved IP is then pinned into `http.Transport.DialContext`,
so a second DNS query mid-request cannot be re-bound to a
different IP. This is the same defense `golang.org/x/net/proxy`
and `tailscale/derp` use, and it is the standard fix documented
in Go's `net/http` package comments.

Resolution itself uses `net.DefaultResolver.LookupIPAddr` with a
context. The lookup is per-request; there is no cache. A
misconfigured DNS that flips between a public IP and a private
IP is caught on every request.

**Test-only escape hatch (added in M4-T5.2, 2026-09-05).** The
`Policy` exposes a `insecureSkipPrivateIPGuard` field (set via
`PolicyOptions.InsecureSkipPrivateIPGuard`, never via the
production `NewPolicy` shorthand). The field's purpose is to
let the unit tests dial `httptest` servers bound to
`127.0.0.1` without weakening the production §21.5 §4
guarantee. The field's name is the friction: any reader sees
the `insecureSkip` prefix and a future contributor who copies
the pattern into production is on notice. The
`cmd/athanor/gateway.go` wire-up never sets the flag. A
future M4-T8 regression test will assert the field is
`false` in the production default.

### 5. Rate limiting: in-process token bucket, per-host

A per-host token bucket, ~30 lines of Go, no new dependency. The
bucket key is `host:port` (a request to `en.wikipedia.org` and
`de.wikipedia.org` are independent buckets; a request to
`en.wikipedia.org` and `evil.wikipedia.org` are also independent —
the per-host key is the same key the allowlist uses, so a
typosquat domain that gets past the allowlist still has its own
bucket and cannot drain the legitimate domain's budget).

Bucket capacity and refill rate come from
`network.rate_limit_per_minute` (default 30, already in
`internal/config/config.go`). Capacity equals the per-minute rate;
refill is continuous at `rate / 60` tokens per second. When the
bucket is empty, the request is refused with
`ErrRateLimited`. Refusal is logged under `category=network,
event=rate_limited`.

The bucket is in-process (one bucket per daemon, not one per
daemon per pod). Operators with multiple daemons on the same
host need to put a process-external limiter in front; the
gateway does not try to be that. Documented inline.

### 6. Header sanitization: fixed deny-list of header *names*

A request leaving the gateway has the following headers stripped
by name (case-insensitive, ASCII-only):

- `Cookie`
- `Authorization`
- `Proxy-Authorization`
- `X-Api-Key`
- `X-Auth-Token`
- `X-Secret`

The deny-list is on header *names* only; header *values* are not
inspected. A header named `X-Safe-Custom` carrying
`Bearer <secret>` passes through. This is intentional: the gateway
is a transport policy layer, not a content scanner. The
prompt-injection `Scanner` (ADR-0015 §"Trust boundaries, not all
text") runs on the *response body*, not on request headers.

The gateway *sets* a fixed `User-Agent` (`Athanor/0.1 (+local
agent)`), regardless of what the caller passed. This is a small
operational kindness to the allowlisted domains' operators and a
forensic signal in the access logs.

### 7. Response size cap: streamed truncation, not failure

`network.max_response_bytes` (default 10 MiB, already in config)
is enforced as a stream-side counter. When the counter exceeds
the cap, the response body is truncated at the cap and the
returned `Response.Truncated` flag is set to `true`. The fetch
does **not** fail. The truncation event is logged under
`category=network, event=truncated`.

Truncation is preferred over failure because the §21.5 use case
("fetch a documentation page, hand readable markdown to the
LLM") is a graceful-degradation case: a 50 MiB page with the
first 10 MiB containing the answer is *useful*; a hard fail on
size is *not*. The Truncated flag is the operator's audit trail.
T6 (Reader Mode) checks the flag and re-fetches with a smaller
cap if the markdown extraction ran out of bytes.

### 8. Logging: every fetch is a `network` event

Every fetch — success, refusal, truncation, rate-limit, error —
appends one event to the append-only log
(`internal/store.AppendEvent`) with:

- `category: "network"`
- `level`: `info` (success), `warn` (refusal / truncation /
  rate-limit), `error` (transport failure)
- `data`: `url`, `resolved_ips`, `allowlist_decision`
  (`allowed` | `denied_off_list` | `denied_private_ip` |
  `denied_idn` | `denied_denylist`), `sanitized_headers`
  (the list of names that were stripped), `status`,
  `bytes_read`, `truncated` (bool), `duration_ms`,
  `request_id` (a UUIDv4 generated per fetch for log
  correlation).

The `network` category already exists in the closed
`internal/config/load.go` Categories set. The schema is the same
`map[string]any` every other category uses; no migration is
needed for T5.

The event append is best-effort. A failure to log does not fail
the fetch — the operator's audit trail is durable, but the
gateway's user-visible behavior is not coupled to the store's
write path. A failure to log is itself logged via `slog` at
`warn` level.

### 9. Cloud inference mediation (§21.7): deferred to M6

The credential-broker pattern documented in §21.7 (credentials
never enter Job Pods; the Core injects them into approved
outbound requests) is part of §21.5's responsibility 7 but
inherently requires the M6 HITL infrastructure. M4-T5 builds the
gateway without credential injection; an outbound request the
gateway makes on behalf of a Job Pod passes through without
secrets. M6 adds the broker.

The T5 `Client` interface has a `WithCredentials(map[string]string)`
method (or equivalent) reserved for M6; the method exists in
T5 as a no-op so the call site compiles, but it does not
attach anything to outbound requests yet. Documented inline.

### 10. Browser Mode (§21.6): unwired in M4-T5

`network.browser_mode_requires_approval` exists in the config
block but is not consulted by the gateway in T5. The HITL
infrastructure is M6. The flag stays declared and defaulted to
true so operators do not silently think it is in effect; an
inline comment in `internal/config/defaults.go` and
`config.example.yaml` (mirroring the dormant-`Execution`-flags
pattern from `f309500`) records the deferral. The T5 ADR
records it; the close-out commit updates both files.

## Consequences

- **One outbound HTTP door.** Adding a new outbound call to
  any package other than `internal/gateway/` is a Gate G1
  rule 6 violation. The `Client` interface is the seam every
  consumer (T7 tools, M6 cloud mediation, future internal
  needs) goes through. Future contributors cannot add a
  parallel egress path without tripping the build.
- **§21.5 responsibilities 1–5 in production; 6 in T6; 7 in M6.**
  Each is a deliberate, single-responsibility commit. The
  interfaces are designed up front so the seams exist; the
  behaviors land in their owning milestones.
- **Allowlist is the operator's primary knob.** Every other
  defense (rate limit, size cap, header sanitization) is a
  backstop. An operator who sets `allow_list: ["*"]` gets a
  working but unsafe gateway; the ADR and the example config
  both document this. The `default_policy: deny` + `allow_list: []`
  default means a fresh clone does not accidentally reach the
  internet.
- **In-process rate limiting is single-tenant.** The per-host
  bucket is correct for "one daemon, one workspace." A future
  multi-tenant deployment (out of M4 scope, M7+ at earliest)
  needs an external limiter; the gateway is documented as
  not trying to be that.
- **The `network` event category was already in the closed set.**
  T5 adds a new `event=...` value to the `data` payload but no
  schema change. The categories list in
  `internal/config/load.go` is unchanged.

## Not in M4-T5

- **M4-T6** (Reader Mode extraction, `go-readability` +
  `bluemonday`): T6 is its own task and its own dep question.
  The T5 client returns a raw `Response{Body []byte}`; T6
  adds the readability pass on top.
- **M4-T7** (gateway-backed tools `fetch_url`, `search_web`):
  T7 is its own task. T5 builds the gateway; T7 wires the
  tool envelope to it.
- **M4-T8** (adversarial security suite): the Gate G4
  close-out. The T8 corpus draws on the tests in
  `internal/gateway/policy_test.go` (the suffix-match corpus,
  the IDN corpus, the private-IP corpus) and the
  `httptest`-driven T5.2 tests.
- **Cloud-inference credential injection** (§21.7 / §21.5
  responsibility 7): M6.
- **Browser Mode** (§21.6): the config flag stays declared;
  the runtime is M6's HITL work.
- **Cross-pipeline correlation between network events and
  artifacts:** a future M5/M6 task could link a fetched
  document to the artifact that consumed it; M4 does not need
  that.

## Forward references

- T6 (Reader Mode) consumes `Client.Fetch`'s `Response.Body`
  and runs `go-readability` + `bluemonday` over it before
  returning markdown. The dep question for those two
  packages is T6's planning, not T5's.
- T7 (tools) registers two new tools in the closed envelope
  (`fetch_url`, `search_web`) that go through the gateway.
  The tool envelope is unchanged in T5; T7 widens it.
- T8 (adversarial suite) treats the T5 corpus as the
  baseline. SSRF attempts in T8 are denial assertions against
  the DNS-rebinding guard; allowlist-bypass attempts are
  denial assertions against the suffix-match evaluator with
  the `.` separator.
- M5 / M6 (Context Engine, Autonomy & Feedback) may
  add new event names under the `network` category
  (e.g. `event=reader_mode_applied` from T6,
  `event=credential_injected` from M6). The category is
  closed; the `event` payload key is open.
