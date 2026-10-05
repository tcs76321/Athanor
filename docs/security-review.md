# Security Review — Internal API & Tool Surface

**Date:** 2026-10-05 · **Scope:** `/internal/v1/` (Job Pod → Core), the §25 tool
envelope, and the external API/UI boundary · **Refs:** ARCHITECTURE §3.1, §21,
§25; Gate G2 (`internal/gate/gate_g2_test.go`)

This is a deliberate route-by-route review prompted by the M3-T7 work (which
found one isolation gap by accident). It reviews each pod-callable route for
authentication, envelope enforcement, and **scope isolation** — the pod is the
untrusted actor, so "does the pod control which scope it touches?" is the core
question.

## Method

Enumerate every route registered in `internal/internalapi/handlers.go`; for each,
check (a) the auth middleware, (b) the per-job tool envelope, (c) whether any
client-supplied identifier selects *another* scope's data, (d) input validation
and bounded sizes.

## Findings

### F-1 — `context_swap` trusted client scope (FIXED)

`handleContextSwap` only defaulted an empty `Scope` to the job; a pod could name
any scope and target any chunk. `mce.ChunkStore.Swap` loaded the target by ID
with no ownership check, and the response returned the bytes to the pod.
**Impact:** cross-scope active-pointer overwrite and cross-project source read.
**Fix:** the handler forces `scope = jobID`; the Core verifies the target chunk
belongs to the job or its project (`mce.ChunkStore.Owns`); a foreign/unknown
chunk is the same 404. Tests: `context_swap_adapter_test.go`.

### F-2 — `query_memory` trusted client scope (FIXED, this review)

`handleQueryMemory` had the identical pattern: an empty scope defaulted to the
job, but a non-empty scope was passed to the store unchecked. A pod could query
**another project's or job's** compacted memory and dormant chunks.
**Impact:** cross-scope memory disclosure. **Fix:** the handler now accepts only
the caller's job id or its project id (resolved via `project.Repo.JobProject`);
anything else is 403 and audited. Test: `TestQueryMemory_RejectsForeignScope`.

### F-3 — `handleJobGet` resolves a *task* by a *job* id (OPEN, latent)

`GET /internal/v1/jobs/{id}` calls `a.projects.Task(ctx, jobID)` — but `jobID`
is a job id and `Repo.Task` loads by task id, so in production the route returns
404. It is masked in tests because the harness uses a task id as the job id, and
the current engine does not call this route (the tool sub-steps pass their
payloads directly). **Recommended fix:** resolve job → task_id → task (a
`Repo.JobTask`, mirroring `Repo.EnvelopeFor`), and update the handler test
harness to seed a real job row. Low urgency (latent), but correctness-relevant
for any future pod that reads its task context at startup.

## What is sound

- **Auth.** Every `/internal/v1/` route is wrapped in `authMiddleware`
  (structurally proven by Gate G2); the bearer compare is constant-time
  (`crypto/subtle`); the token is per-job, tmpfs-delivered, revoked at teardown.
- **Envelope.** Every tool route consults `a.tools.EnvelopeFor` and refuses
  (403, audited) when the tool is not in the per-job set; Gate G2's
  bypass test enforces this structurally.
- **`execute_code`** validates `language` against the closed set (`python`).
- **`run_tests` / `lint`** accept an operator/pod command that runs inside the
  same pod — not a capability beyond the pod's existing isolation.
- **Gateway tools** validate an absolute http(s) URL and defer to the §21.5
  policy; response caps and the fail-closed reader apply.
- **Bounded input.** Handlers use `http.MaxBytesReader` (1 MiB shared, tighter
  per-route); the search query is capped (`maxQueryBytes`).
- **External surface** is loopback-only (§21.8) with the ADR-0011 Host-header
  allowlist (now actually wired — see the boot fix).
- **Audit.** Allow/deny is recorded per tool call in the append-only event log.

## Recommendations

1. Fix F-3 (`Repo.JobTask`) and update the harness to use real job ids.
2. Add a **structural gate** for scope isolation: a test asserting every
   scope-carrying request struct (`ContextSwapRequest`, `QueryMemoryRequest`)
   is scope-constrained in its handler — so a future scope-trusting field
   cannot land silently.
3. Consider a general rule: **no pod-supplied identifier may select a scope**;
   scopes are always derived server-side from the authenticated job.
