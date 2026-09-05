# M4-T5 — Internet Gated Reader (Demo Script)

This is the executable proof for the §21.5 gateway: every
outbound HTTP request the Core makes goes through
`internal/gateway/`, the allowlist + denylist + DNS-rebinding
guard are enforced *before* the transport is invoked, and
every fetch leaves one `network` event in the append-only
log. The script is `httptest`-based by default (no internet
required); the `ATHANOR_RUN_GATEWAY_NETWORK=1` env var opts
in to a real-domain fetch against one allowlisted host.

M4-T5 consists of three commits: T5.1 (the pure
`Policy` skeleton, `ab082e8`), T5.2 (the runtime
`Client` + daemon wiring, `225ed38`), and T5.3 (this
close-out, `docs:` commit). The first two are the
code; this document is the operator's view of the
same surface.

## What Gate G4 *will* prove (after T6–T8)

| Property | Structural proof (CI) | Behavioral proof (local) |
|---|---|---|
| Outbound HTTP lives only in `internal/gateway/` | `TestGateG1NoOutboundHTTPOutsideGateway` (T5.4) | — |
| Allowlist is suffix-match with `.` separator | `TestMatchHost` in `internal/gateway/policy_test.go` (the `evilwikipedia.org` gotcha) | `TestGateway_PolicyAllowsSubdomainButNotTyposquat` (T8) |
| Denylist overrides allowlist | `TestEval_DenyListOverridesAllow` | `TestGateway_DenyListWinsOverAllow` (T8) |
| DNS-rebinding guard refuses private/loopback/link-local/CGN | `TestEval_DenyPrivateIP` (13 rows) | `TestGateway_DNSRebindAttemptFails` (T8) |
| IDN fails closed (T5 ships ASCII-only) | `TestEval_DenyIDN` | — |
| Per-host rate limit denies after N requests | `TestFetch_RateLimitedAfterBudget` | `TestGateway_RateLimitCap` (T8) |
| Cookie / Authorization / X-Api-Key / etc. stripped on the wire | `TestFetch_StripsDenyListedHeaders` | `TestGateway_NoCredentialLeak` (T8) |
| Fixed `User-Agent` set unconditionally | `TestFetch_SetsFixedUserAgent` | — |
| Response size cap truncates + flags, does not fail | `TestFetch_TruncatesOversizedResponse` | — |
| Dial uses the policy's resolved IPs (no re-resolve) | `TestFetch_PinnedIPDialContext` | `TestGateway_PinnedIPReachesServer` (T8) |
| `network` event covers every outcome | `TestFetch_AuditShape` | — |

The structural half runs in CI on every push. The
behavioral half is opt-in: the demo below exercises
the structural tests plus the `httptest`-based unit
tests in `internal/gateway/`; the **T8 adversarial
suite** is opt-in via `ATHANOR_RUN_INTEGRATION=1` (a
real allowlisted domain is required).

## Prerequisites

- Go 1.26+ with CGO (see [DEVELOPMENT.md](../DEVELOPMENT.md))
- A working `make` (the standard `make check` aggregate)
- For the real-network opt-in: a reachable HTTP
  endpoint whose host is in your `network.allow_list`

## The structural proof (≈ 5 seconds)

```bash
# 1. Confirm the gateway's policy corpus is green. This
#    runs ~13 Test* functions in internal/gateway; the
#    suffix-match gotcha, the IDN rejection, every
#    non-routable CIDR range, the deny-list override,
#    the default-allow escape hatch, and the
#    "default-deny never consults the resolver"
#    durability property are all table-driven.
make check
```

Expected output (truncated):

```
go test -race ./...
ok  	github.com/tcs76321/athanor/internal/gateway	1.331s
ok  	github.com/tcs76321/athanor/internal/gate	1.321s
```

The `internal/gate` package is Gate G1: the AST walk
that fails the build if any production source under
`internal/` or `cmd/` imports `os/exec`, container
clients, or raw `syscall` outside the allowlisted
files. T5 does not add rule 6 (the AST walk for
`http.Get` / `http.Post` / `http.DefaultClient`
outside `internal/gateway/`); rule 6 lands in **T5.4**
as part of the **T8** adversarial suite.

## The runtime proof (≈ 10 seconds)

The §21.5 flow's full path — `Policy.Eval` → per-host
rate limit → header sanitization → per-request
transport with pinned `DialContext` → streamed
response size cap → `network` event — is covered by
~14 `Test*` functions in
`internal/gateway/client_test.go`. The most
representative is `TestFetch_HappyPath`:

```bash
# Run just the runtime Fetch tests (the test that
# exercises the §21.5 flow end-to-end against
# httptest).
CGO_ENABLED=1 go test -race -count=1 -v -run 'TestFetch_' \
    ./internal/gateway/
```

Expected output (truncated; ~14 tests, all green):

```
=== RUN   TestFetch_HappyPath
--- PASS: TestFetch_HappyPath (0.00s)
=== RUN   TestFetch_StripsDenyListedHeaders
--- PASS: TestFetch_StripsDenyListedHeaders (0.00s)
=== RUN   TestFetch_SetsFixedUserAgent
--- PASS: TestFetch_SetsFixedUserAgent (0.00s)
=== RUN   TestFetch_TruncatesOversizedResponse
--- PASS: TestFetch_TruncatesOversizedResponse (0.00s)
=== RUN   TestFetch_RateLimitedAfterBudget
--- PASS: TestFetch_RateLimitedAfterBudget (0.00s)
=== RUN   TestFetch_PolicyDeniedDeniesBeforeTransport
--- PASS: TestFetch_PolicyDeniedDeniesBeforeTransport (0.00s)
=== RUN   TestFetch_PrivateIPRefused
--- PASS: TestFetch_PrivateIPRefused (0.00s)
=== RUN   TestFetch_InvalidURLAudited
--- PASS: TestFetch_InvalidURLAudited (0.00s)
=== RUN   TestFetch_NilRequest
--- PASS: TestFetch_NilRequest (0.00s)
=== RUN   TestFetch_TransportErrorAudited
--- PASS: TestFetch_TransportErrorAudited (0.00s)
=== RUN   TestFetch_AuditShape
--- PASS: TestFetch_AuditShape (0.00s)
=== RUN   TestFetch_PinnedIPDialContext
--- PASS: TestFetch_PinnedIPDialContext (0.00s)
=== RUN   TestNewClient_Validation
--- PASS: TestNewClient_Validation (0.00s)
```

## The daemon wiring proof

The gateway is constructed at boot by
`cmd/athanor/gateway.go`, which calls
`gateway.NewPolicy` from `cfg.Network` and
`gateway.NewClient` with the production
`NetworkResolver{}` zero value (delegates to
`net.DefaultResolver`). A failure to construct is
fatal; the operator sees a clear startup error
rather than a silent "no gateway" runtime.

```bash
# Confirm the daemon wires the gateway at boot.
CGO_ENABLED=1 go build -o bin/athanor ./cmd/athanor

# Start the daemon with a fresh state dir and the
# default config (no allow_list, default_policy=deny).
# The slog line "gateway: constructed" appears in
# the boot output.
./bin/athanor serve -state-dir /tmp/athanor-demo -addr 127.0.0.1:7421
```

Expected output (the gateway line, truncated):

```
time=... level=INFO msg="gateway: constructed" default_policy=deny allow_list_size=0 rate_limit_per_minute=30 max_response_bytes=10485760
```

A subsequent `GET /jobs/<id>/events?category=network`
query returns the full `network` event chain for any
fetch the gateway performed. With the default
`allow_list=[]`, every fetch is denied at the policy
layer and the audit log records one `event=denied`
row per attempt.

## The real-network opt-in (M4-T8 territory)

The T5 commit's behavioral coverage is fully
`httptest`-based — no real internet is touched. The
T8 adversarial suite will introduce a
`ATHANOR_RUN_GATEWAY_NETWORK=1` env var that flips
the suite to real fetches against one allowlisted
domain. The T5 plan is to land the structural half
of the proof today and let T8 carry the real-network
half; a release-tagged binary should not be
considered "research-safe" until T8 is green.

## What this demo does NOT prove

- **Reader Mode extraction** (M4-T6,
  `go-readability` + `bluemonday`).
- **Gateway-backed tools** (`fetch_url`,
  `search_web`; M4-T7).
- **Adversarial security suite** (M4-T8 — SSRF
  attempts, allowlist-bypass attempts, header
  leak probes, rate-limit flood).
- **Cloud-inference credential injection** (M6 /
  §21.7).
- **Browser Mode** (M6 / §21.6).

Each of these has a clear path in
[ROADMAP.md](../ROADMAP.md) and is documented as
"next" in the README. The T5 close-out ships
responsibilities 1–5 of §21.5 and pins them with
~30 `Test*` functions; the remaining
responsibilities (6, 7) and the adversarial corpus
(T8) are the next milestones.
