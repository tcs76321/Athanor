# Developing Athanor

Short guide for working on this repository. The authoritative design lives in [`ARCHITECTURE.md`](ARCHITECTURE.md); the build order in [`ROADMAP.md`](ROADMAP.md).

## Build & test

**CGO is required.** `internal/store` uses `mattn/go-sqlite3` so sqlite-vec can be loaded at runtime later (task-000 findings: `docs/sqlite-setup.md`, decision: [ADR 0003](docs/adr/0003-sqlite-driver-cgo-single-connection.md)).

```bash
make build        # CGO_ENABLED=1 go build ./...
make test         # go test ./...
make test-race    # go test -race ./...
make vet          # go vet ./...
make run          # run the daemon locally
```

Don't run bare `go build` / `go test` — they silently drop CGO and fail on the sqlite3 driver. The Makefile sets the flag for you, and CI enforces the same targets.

### Dependencies

The project stays deliberately lean. Every direct dependency is pinned
by an executable allowlist in `internal/deps/deps_test.go`
(`allowedDirectDeps`), so a dependency cannot be added or removed
without a deliberate, reviewable edit to that list. Today there are
ten direct deps:

| Package | Why |
|---|---|
| `mattn/go-sqlite3` | persistent state (CGO, ADR-0003) |
| `gopkg.in/yaml.v3` | config loading |
| `github.com/fsnotify/fsnotify` | §21.3 ingress filesystem watcher (M4-T2) |
| `codeberg.org/readeck/go-readability/v2` | §21.5 Reader Mode extraction (M4-T6). The maintained continuation of the deprecated go-shiori module; see [ADR-0018](docs/adr/0018-reader-mode.md) §1. Only `FromReader` is ever called — `FromURL` would open a second egress path past the gateway, and Gate G1 rule 6 makes that a build break. |
| `github.com/microcosm-cc/bluemonday` | Reader Mode sanitization (UGCPolicy) before markdown rendering |
| `golang.org/x/net` | HTML parsing for Reader Mode (`net/html`) — direct import since M4-T6; kept at ≥ the patched release for CVE-2026-25680 |
| `github.com/tree-sitter/go-tree-sitter` | §10.1 lossless division runtime (M5-T2; ADR-0020/ADR-0021) |
| `github.com/tree-sitter/tree-sitter-go` | Go grammar for the divider |
| `github.com/tree-sitter/tree-sitter-python` | Python grammar for the divider |
| `github.com/tree-sitter/tree-sitter-javascript` | JavaScript grammar for the divider |

Adding a dependency is a project decision (AGENTS.md), not an agent
decision — surface it in the plan. Ratification is adding the module
path to `allowedDirectDeps` in the same commit.

### Integration (behavioral) security probes

The behavioral probes are opt-in: they are gated by the
`ATHANOR_RUN_INTEGRATION=1` env var, bring up **real** rootless Podman
pods, and (for the gateway probes) reach the **real** internet. They are
deliberately outside `make check` (which must stay fast and hermetic) and
run instead in a dedicated, **non-blocking** CI job — `integration` in
`.github/workflows/ci.yml`.

| Probe | File | Needs |
|---|---|---|
| Five pod-hardening probes (network, Ollama, podman.sock, host FS, credentials) | `internal/jobpod/security_test.go` | `podman` + `alpine:3.20` |
| M2-T4b exec probe (`TestExec_Integration_RealPod`) | `internal/jobpod/exec_integration_test.go` | `podman` + `python:3.12-alpine` |
| Two M4-T8 gateway probes (default-deny; allowlisted fetch + extract) | `internal/gateway/integration_test.go` | internet access (`example.com`) |

Run them locally exactly as CI does:

```bash
make integration-images               # pulls alpine:3.20 + python:3.12-alpine
make test-integration                 # ATHANOR_RUN_INTEGRATION=1, both packages
```

`make test-integration` covers **both** packages — `internal/jobpod/...`
and `internal/gateway/...` — so the gateway probes are no longer
orphaned. Reference runs: 2026-08-30 (pod probes, macOS 14 / podman 5.8.2
/ applehv), 2026-09-15 (gateway probes), and 2026-09-30 (exec probe,
macOS / podman 6.0.2 / libkrun) — see
[`docs/demo-m2.md`](docs/demo-m2.md) and
[`docs/demo-m4-t8.md`](docs/demo-m4-t8.md).

The structural tests (the M2 argv + envelope gates, and the M4-T8 SSRF /
bypass / content corpora) still run in `make check`; the integration
probes are the behavioral double-check, now executed on Ubuntu in CI as
well as on a developer's machine. The `integration` job starts
`continue-on-error` (non-blocking) so it cannot break `main` while the
runner's rootless-Podman setup is proven; it is promoted to a required
check once it has been observed green on `main`.

FTS5 is compiled into the build via the `sqlite_fts5` build tag, which the
Makefile exports as `GO_TAGS` for every build and test target and which CI
passes to golangci-lint (`--build-tags sqlite_fts5`). Never build or test
with a bare `go` command: a tag-less binary fails the `store.CheckFTS5`
boot preflight with a named error before any migration runs (ADR-0026 §1).

## Per-phase budgets

Default `execution.phase_wall_time_budgets` are sized for 7–12 B models at
~0.7 temperature. A 27 B thinking-mode model on first call (cold load)
can take 2–3 min for `planning`, which will exceed the 300 s default.
If you see `context deadline exceeded` in `planning`, raise the budget in
your `config.yaml` (e.g. `planning: "600s"`) or use
[`config-probe.yaml`](config-probe.yaml), which already does this for a
single-model setup. Findings: [`docs/probes/m1-quality-probe.md`](docs/probes/m1-quality-probe.md).

## Running the daemon

```bash
make run          # equivalent to: go run ./cmd/athanor
```

Daemon flags: `-config config.yaml` · `-addr 127.0.0.1:7420` · `-state-dir state`. The HTTP surface binds **loopback only** (ARCHITECTURE §21.8); non-loopback addresses are rejected at startup. Check `http://127.0.0.1:7420/healthz` for `{status, version, uptime}`. Boot order is fixed: config → logging → store → migrations → kill switch → engine → serve → recover active jobs; SIGINT/SIGTERM triggers graceful shutdown, and every start/stop is recorded in the append-only event log.

**No config file yet?** The daemon boots on built-in defaults and logs a notice — only *absence* falls back; a present-but-invalid `config.yaml` still fails loudly with a specific error. `config.example.yaml` in the repo root documents every option with its default value (a test enforces that the example always matches the built-in defaults, so copying it is a no-op starting point), and `athanor init` writes a marshaled default config for you.

## CLI (M1)

Client commands talk to a running daemon (default `http://127.0.0.1:7420`, override with `-addr`):

```bash
athanor project create -name demo -archetype code -goal "..." [-criteria "a;b"] [-repo <dir>] [-test-command "go test ./..."] [-build-command "go build ./..."]
athanor goal submit -project <id> -goal "..."
athanor goal decompose -project <id> -goal "..." [-criteria "a;b"]  # M6-T1: validated task DAG, no execution yet
athanor job watch -job <id>            # prints each phase transition, then the artifact
athanor artifacts -project <id>
athanor index -project <id> [-path <dir>]  # index the project's repository (M5-T8, ADR-0028)
athanor hitl list                      # M6-T4: pending HITL requests
athanor hitl approve -id <id> [-note "..."]   # resume the parked job
athanor hitl reject -id <id> [-note "..."]    # fail it
athanor hitl defer -id <id> -for 1h           # extend the pending window
athanor freeze                         # §22 kill switch; frozen state survives restarts
athanor unfreeze -reason "..."         # requires a reason; recorded in the event log
```

`execution.dag_decomposition: true` switches `goal submit` from the M1 single-task path to decompose-then-schedule over the M6-T2 dependency scheduler ([ADR-0033](docs/adr/0033-dependency-scheduler.md)); the default is off.

The full M1 walkthrough is [`docs/demo-m1.md`](docs/demo-m1.md) — it doubles as the Gate G1 demo script.

## Repository layout

| Path | Role |
|---|---|
| `cmd/athanor/` | daemon entry point + CLI client commands |
| `internal/` | real implementation packages (config, logging, store, power, llm, prompt, job, artifact, project, engine, control, api, server, gate, …) |
| `migrations/` | embedded forward-only SQL migrations (`NNNN_description.sql`) |
| `spikes/` | **throwaway** validation code. Prove or kill an idea here, record findings under `docs/`, then implement properly in `internal/`. Spike code never gets imported by `internal/`. |
| `docs/adr/` | Architecture Decision Records |
| `docs/demo-m1.md` | Gate G1 demo script (executable walkthrough) |
| `docs/probes/` | quality-probe protocols and findings |

## Workflow conventions

- **Commits:** one per roadmap task, titled `M#-T#: <title>`; docs/infra changes use plain prefixes (`docs:`, `ci:`, `chore:`).
- **ADRs:** any decision future-you would ask "why did they do *that*?" about gets a short ADR in `docs/adr/NNNN-title.md` (context → decision → consequences).
- **Roadmap:** update the status table as milestones progress — it is the project's honest heartbeat.
- **Spikes:** timeboxed, with an explicit hypothesis. Findings land in `docs/`; the spike itself stays disposable.

## License

AGPL-3.0 — see [LICENSE](LICENSE).
