# ADR 0036 — HITL-gated `git_push` (M6-T5)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §14,
§20.1, §25; ROADMAP M6-T5; ADR-0030 (Git-as-undo), ADR-0035 (HITL queue).

## Context

ARCHITECTURE §25 lists `git_push(repo, remote)` as a tool that **requires
HITL approval**, and §14 requires approval before any push. F3-T5
([ADR-0030](0030-git-as-undo.md)) landed the Core-side commit half of
Git-as-undo: accepting an artifact commits it to `athanor/<project>` through
`cmd/athanor/git_client.go` — the only `os/exec` Git site — and **never
pushes**. M6-T4 ([ADR-0035](0035-hitl-queue.md)) gave that a real approval
queue.

M6-T5 must connect the two: a push attempt creates an approval request, and
a denied push leaves the remote untouched.

## Decision

### 1. `git_push` is a Core action, not a pod tool

The Core owns Git: the repository, the branch, and the remote stay on the
host, and a Job Pod never sees them (§3.1). `git_push` is therefore **not**
added to the per-job tool envelope; it is a Core-initiated action, exactly
like the F3-T5 commit. The push executes through the same Gate-G1-allowlisted
`cmd/athanor/git_client.go`, extended with a `Push` method.

### 2. A push attempt is a HITL request, not a parked job

`POST /projects/{id}/push` (and `athanor push -project <id> [-remote]`)
creates a `git_push` request whose payload carries the project, remote, and
the agent branch. There is **no job in `awaiting_approval`**: a push is
operator- or engine-initiated and is not itself a task execution. The request
has no default TTL; an operator may defer it, and an explicit TTL would deny
by expiry like any other request.

### 3. Approval executes the push through the queue's approver seam

`hitl.Service` gains an `Approver` seam: a side effect registered per request
type and invoked only on **approve**. The daemon registers a `git_push`
approver that resolves the project, runs `git push <remote> <branch>`, and
audits the outcome (`git_pushed` / `git_push_failed`). A rejected or expired
request never invokes the approver, so the remote is untouched. Approval with
no registered approver is a no-op beyond the recorded decision — it cannot
silently push.

### 4. Job-linked requests keep their existing behavior

The approver runs only for approved requests whose type is registered. A
job-linked request (a future `Service.Await` caller) still resumes its job
through `applyOutcome`; the two paths are independent and compose if a job
later both resumes and requests a push.

## Consequences

- The §25 `git_push` HITL requirement is enforced end-to-end: no approval, no
  push; approval records who decided and when, and the push outcome is
  audited.
- A denied or ignored push cannot alter the remote; the agent branch simply
  stays local (the F3-T5 state).
- `git_push` remains out of the pod tool envelope, so a compromised pod still
  cannot reach a remote even indirectly.
- The first *job-linked* push can adopt `Service.Await` later without changing
  this design.

## Implemented (M6-T5.2–T5.3)

`hitl.Service` gained a per-type `Approver` seam invoked only on approval,
and `internal/hitl` added `TypeGitPush`. `cmd/athanor/git_client.go` gained
`Push` (with an injectable runner); `cmd/athanor/git_push.go` provides
`gitPusher` (creates the request) and `gitPushApprover` (runs the push and
audits `git_pushed` / `git_push_failed`). `POST /projects/{id}/push` and
`athanor push` create the request, and serve wires the approver. A rejected
or expired request never invokes the approver, so the remote is untouched.
