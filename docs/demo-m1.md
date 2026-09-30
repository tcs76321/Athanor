# M1 Walking Skeleton — Demo Script (Gate G1)

This is the executable proof for **Gate G1**: a goal goes in, an
LLM-generated draft artifact comes out, the run survives a crash, and the
agent provably has no tool-execution capability.

Everything below is verified against the M1 codebase; it mirrors the
automated E2E test `TestEndToEndWalkingSkeleton`
(`internal/api/e2e_test.go`), so CI re-proves the demo on every push.

## Prerequisites

- Go 1.27+ with CGO (see [DEVELOPMENT.md](../DEVELOPMENT.md))
- [Ollama](https://ollama.com) running locally. The default `personas:`
  block assumes five models; if you only have one, copy
  [`config-probe.yaml`](../config-probe.yaml) and use that as your
  `-config` instead — it maps every role to a single model. See
  [`docs/probes/m1-quality-probe.md`](../probes/m1-quality-probe.md) for
  the per-model findings behind the recommendation.
- A config pointing `inference.ollama_url` at your Ollama: the default
  URL assumes the Core runs inside a container (M2), so on a dev host
  run `athanor init` and set
  `inference: { ollama_url: "http://localhost:11434" }`.

## The demo (≈5 minutes)

```bash
# 0. Build, write a local config (then edit ollama_url as above), run
make build
./bin/athanor init        # writes ./config.yaml with every default
make run                  # daemon on http://127.0.0.1:7420
# in another shell:
curl http://127.0.0.1:7420/healthz

# 1. Create a project (name + archetype + goal ≥ 20 chars)
./bin/athanor project create \
  -name my-essay \
  -archetype text \
  -goal "Write a short essay about why local-first software matters." \
  -criteria "at least three arguments;a conclusion"

# 2. Submit a goal — this creates the task, a queued job, and starts it
./bin/athanor goal submit \
  -project <project-id-from-step-1> \
  -goal "Summarize the essay in exactly five bullet points."

# 3. Watch the job walk the skeleton, phase by phase
./bin/athanor job watch -job <job-id-from-step-2>
# → queued → context_building → planning → diverging
#   → synthesizing → comparing → completed
# → artifact: <artifact-id>

# 4. Inspect the artifacts (a draft proposal + the final draft document)
./bin/athanor artifacts -project <project-id>

# 5. Prove the kill switch (§22): freeze, watch new work get rejected,
#    unfreeze with a logged reason
./bin/athanor freeze
curl -X POST http://127.0.0.1:7420/projects/<id>/goals \
  -d '{"goal": "Another goal while frozen is rejected."}'
# → 409 conflict
./bin/athanor unfreeze -reason "demo complete"
```

## Crash recovery (§23.6)

```bash
# While a job is mid-flight (start a long goal, then):
kill -9 <daemon-pid>
make run     # restart
./bin/athanor job watch -job <job-id>
# The job resumes from its last committed phase and completes; the
# recovery_flag "interrupted" appears while resuming and clears once the
# job makes progress.
```

Automated equivalents: `TestCrashRecoveryResumesFromLastCommittedState`
(`internal/job`), `TestRecoverResumesMidFlightJob` (`internal/engine`).

## Containment proof (Gate G1)

```bash
CGO_ENABLED=1 go test ./internal/gate/
```

`TestGateG1NoToolExecution` walks the AST of every production source
under `internal/` and `cmd/` and fails if any file imports a
tool-execution capability (`os/exec`, container clients, raw syscalls in
agent code). The agent in M1 can only call its Ollama endpoint and
persist to SQLite + `state/artifacts/` — nothing else exists.

## Where things live

| Concern | Location |
|---|---|
| Phase executor | `internal/engine` |
| Job state machine | `internal/job` + migration 0004 ([ADR-0006](adr/0006-job-state-enforcement.md)) |
| Artifacts | `internal/artifact` (content in `state/artifacts/`, SHA-256 hashed) |
| Personas + Ollama client | `internal/llm` |
| Prompts | `internal/prompt` (deterministic, §11 order) |
| Kill switch | `internal/control` + `POST/DELETE /freeze` |
| HTTP API | `internal/api` (loopback only, §21.8) |
| Event log | `events` table (append-only) — query via `GET /jobs/{id}/events` |
