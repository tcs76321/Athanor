# M4-T8 — Adversarial Security Suite (Demo Script)

This is the executable proof for M4-T8 (ROADMAP §4: *"All attacks
fail closed; events logged under `airlock`/`network` categories"*,
§31.3). The suite attacks the §21.5 gateway and the airlock scanner
the way an adversary would and proves every refusal is typed,
audited, and fail-closed.

Commits: T8.1 (`1ab9ba5` SSRF suite), T8.2 (`7939b0f` allowlist-bypass
corpus), T8.3 (`1fc3f22` content-attack corpus + two scanner
hardening fixes), T8.4 (this close-out + behavioral probes).

## Attack families → proofs

| Attack | Suite file | Fails closed how |
|---|---|---|
| Non-routable CIDR coverage (every deny-listed range at its boundaries) | `ssrf_test.go` `TestSSRF_NonRoutableCIDRBoundaries` | `isNonRoutable` refuses all sampled IPs; public controls stay routable |
| DNS rebinding across redirect hops | `TestSSRF_RebindAcrossRedirectHops` | Per-hop `Policy.Eval` (the M4-T7 hop loop) sees the flipped answer → `ErrDeniedPrivateIP` + `private_ip` event |
| Allowlisted→allowlisted redirect with flipped DNS | `TestSSRF_RedirectToPrivateHostDenied` | Refused before the dial; hop index on the audit row |
| Integer-host IP obfuscation (decimal / hex / dotted-octal) | `TestSSRF_IPObfuscationHosts` | Two layers: allowlist refusal under defaults; CIDR guard refusal under the allow-all escape hatch (resolver fakes getaddrinfo's integer parsing) |
| Test-only escape hatch leaks into production | `TestSSRF_ProductionWireNeverSkipsPrivateIPGuard` | Behavioral: `NewPolicy` keeps the guard armed; structural: no `cmd/athanor` file may reference `InsecureSkipPrivateIPGuard` / `NewPolicyWithOptions` (the ADR-0017 §4 promise) |
| Allowlist bypass: typosquat, embedded suffix, userinfo, trailing dot, CRLF, empty host | `bypass_test.go` `TestBypass_PolicyDecisions` | Typed denials with matching `network` audit rows (`TestBypass_DenialsAreAudited`) |
| Scheme smuggling (`file:` / `javascript:` / `data:` / `gopher:` / `ws:`) | `TestBypass_PolicyDecisions` | `ErrInvalidURL` — fail closed |
| Deny-list override on a redirect hop | `TestBypass_DenyListWinsOnRedirectHop` | `ErrDeniedDenyList` at hop 1, audited |
| **Positive controls** (uppercase host, explicit port, subdomain) | `TestBypass_PolicyDecisions` | Pinned as *allowed* — an over-broad fix would be its own regression |
| LLM-targeted prompt injection (10 payloads: override, role reassignment, ChatML, role markers, safety bypass, base64 blob) | `hostile_content_test.go` `TestContentAttack_InjectionCorpusFailsClosed` | `ErrPromptInjection`; no markdown leaves; `reader_mode_rejected` audited |
| Injection rides sanitization as plain text | `TestContentAttack_InjectionSurvivesSanitization` | Caught by the scan (the last mandatory pipeline step), not the sanitizer |
| Gzip decompression bomb (~10 MiB through a small stream) | `TestContentAttack_GzipBombTruncates` | Streamed cap truncates at exactly the cap, flags `truncated`, audits `event=truncated`; no OOM, no failed fetch |
| Redirect-loop bomb | `TestContentAttack_RedirectChainBombIsCapped` | `ErrTooManyRedirects` after the cap; every attacker-spent hop audited |
| **Scanner weaknesses found by the corpus** | `heuristic_test.go` | Fixed in T8.3: the role-reassignment pattern lacked the multiline flag (prefix lines evaded it) and the base64 detector demanded a clean whole-run decode (chained padded blobs evaded it) — both now fail closed, pinned by new scanner-corpus rows |
| Real network: default-deny + allowlisted fetch | `integration_test.go` (gated) | See below |

## Prerequisites

- Go 1.26+ with CGO (see [DEVELOPMENT.md](../DEVELOPMENT.md))
- `make check` (lint + vet + race tests)
- For the behavioral probes: internet access and
  `ATHANOR_RUN_INTEGRATION=1`

## The structural proof (≈ 5 seconds)

```bash
make check
```

Every suite in the table above runs in CI. Zero skips, zero network.

## The behavioral proof (needs internet)

```bash
ATHANOR_RUN_INTEGRATION=1 CGO_ENABLED=1 go test ./internal/gateway/ -run Integration -v
```

Two probes:

1. **Default-deny holds against a real domain.** A policy-built
   gateway with the shipped defaults (deny, empty allowlist) fetches
   `https://example.com/` → refused with `ErrDeniedOffList`, one
   `network` audit row (`decision=denied_off_list`).
2. **An allowlisted real domain fetches end-to-end.** `allow_list:
   [example.com]` → real DNS → real TLS → Reader Mode extraction →
   markdown + one `reader_mode_applied` audit row.

**Reference run 2026-09-15** (macOS, residential network): both
probes pass in ~0.25 s. The Job Pod side of "no egress" is covered
by the M2 behavioral probes (docs/demo-m2.md): a pod with
`--network=none` cannot reach the internet at all, so the gateway is
the only door — and this suite proves the door only opens for
allowlisted, non-rebinding, non-private destinations with clean
content.

## Gate G4 — closed

Gate G4 (ROADMAP §6): *"Adversarial suite green; a research goal
completes end-to-end using only allowlisted domains."*

- **Adversarial suite green**: `make check` — the SSRF, bypass, and
  content corpora are CI tests (no skips, no network); the behavioral
  probes pass on a developer machine (reference run above).
- **Research goal end-to-end on allowlisted domains only**:
  `TestDiverge_InjectsResearchIntoCandidates` (automated, in CI) and
  the operator walkthrough in [demo-m4-t7.md](demo-m4-t7.md) §"The
  daemon walk".

## What the suite found (and fixed)

The adversarial corpus is not ceremony — it caught two real
weaknesses on its first run, both fixed in T8.3:

1. **Role-reassignment evasion.** The heuristic's "you are now"
   pattern was start-of-*text* anchored; any attacker line before
   the payload slipped past. Now multiline-anchored (line start,
   deliberately — mid-line "the file you are now reading" is benign).
2. **Base64 decode-evasion.** The ≥1 KiB base64-blob check demanded
   a clean whole-run decode; chaining padded blobs into one run
   (mid-stream `==`) defeated it. Shape + size is the gate now;
   decodability is a recorded detail, not a requirement.