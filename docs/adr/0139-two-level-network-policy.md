# ADR-0139 — Two-level network policy and URL-base grants

**Status:** accepted · **Date:** 2026-11-17 · **Refs:** ARCHITECTURE §21.5;
[ADR-0017](0017-gateway.md), [ADR-0035](0035-hitl-queue.md)

## Context

The gateway had one global allowlist and a default-deny policy. That is a sound
ceiling, but it made ordinary research expensive: every new source needed a
config edit and a Core restart, and the UI could only file a request telling
the operator to do that by hand. At the same time, a project-specific allow
could not be expressed at all.

The operator asked for both levels — an agent/global policy and a project
policy — with built-in defaults, a local threat-intel path, and a way for the
agent to request a URL base.

## Decision

1. **Precedence.** Evaluation is:

   ```text
   global deny > project deny > project grant > global allow > default
   ```

   The reserved/private/metadata CIDR guard runs after an allow and always
   wins. A grant never overrides a global deny. A project deny is more specific
   than a global allow.

2. **Grants are URL bases.** A grant is `scheme://host/path-prefix`, matched on
   the exact host (not a subdomain), a cleaned path, and an optional expiry.
   Cleaning collapses `..`, so `/v1/../admin` cannot widen `/v1/`. Grants live
   in `network_grants` (migration 0051) and are project-scoped.

3. **HITL is the approval path.** A `network_access` request carries the
   scheme, host, path prefix, reason, and TTL. Approving it creates a grant
   (`cmd/athanor/netgrant.go`); rejecting or letting it expire changes nothing.
   The UI's project-page request flow now writes this payload instead of
   instructing a config edit.

4. **Profiles are opt-in data.** `network.profile` selects `local-only`
   (default), `research`, or `custom`. A profile's entries merge with the
   operator's `allow_list`/`deny_list`; it never changes `default_policy` and
   never disables the guard. The research profile includes search engines and
   common research/docs sources; allowlisting a search engine does **not**
   allow its result hosts, which still need a grant.

5. **The blocklist is local and explicit.** `network.blocklist_path` points at
   a host/IP/CIDR file, read at boot. Hosts join the deny list; IPs/CIDRs are
   checked against the resolved addresses. Athanor does not download threat
   intel: automatic feed fetching would add an outbound dependency to the
   egress boundary. A bundled static "known malicious IP" list is deliberately
   not shipped because it goes stale; the stable controls are the private/
   metadata guard, the curated baseline deny set, the optional local file, and
   content/trust defenses.

6. **The scope follows the job.** The internal API stamps the authenticated
   job ID onto the gateway request (`json:"-"`, so a pod cannot choose another
   project). The adapter resolves the job's project and active grants and calls
   `FetchScoped`; redirect hops re-evaluate the same scope.

## Consequences

- A research project can add a source through one approval, without editing
  config or restarting the Core.
- The global profile remains the ceiling; an operator can audit and revoke a
  project grant without touching it.
- Every fetch still runs the pinned-IP dial and the private/metadata guard.
- The agent auto-request on a policy denial is the next slice; the request
  payload, approver, storage, and scoped fetch are live now.
