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

### Dependencies (M4-T6)

The project stays deliberately lean — four in-tree dependencies:

| Package | Why |
|---|---|
| `mattn/go-sqlite3` | persistent state (CGO, ADR-0003) |
| `gopkg.in/yaml.v3` | config loading |
| `codeberg.org/readeck/go-readability/v2` | §21.5 Reader Mode extraction (M4-T6). The maintained continuation of the deprecated go-shiori module; see [ADR-0018](docs/adr/0018-reader-mode.md) §1. Only `FromReader` is ever called — `FromURL` would open a second egress path past the gateway, and Gate G1 rule 6 makes that a build break. |
| `github.com/microcosm-cc/bluemonday` | Reader Mode sanitization (UGCPolicy) before markdown rendering |

Adding a dependency is a project decision (AGENTS.md), not an agent decision.

### Integration (behavioral) security probes

The five behavioral probes in `internal/jobpod/security_test.go`
(network, Ollama, podman.sock, host FS, credentials) bring up real
hardened pods and assert denial at runtime. They are gated by the
`ATHANOR_RUN_INTEGRATION=1` env var and **never run in CI**:

```bash
ATHANOR_RUN_INTEGRATION=1 make test-integration
```

The two gateway probes in `internal/gateway/integration_test.go`
(M4-T8) use the same gate: default-deny against a real domain, and an
allowlisted real domain fetched + extracted end-to-end. Reference
runs: 2026-08-30 (pod probes, macOS 14 / podman 5.8.2 / applehv) and
2026-09-15 (gateway probes) — see [`docs/demo-m2.md`](docs/demo-m2.md)
and [`docs/demo-m4-t8.md`](docs/demo-m4-t8.md).

Reason: the probes require a running `podman` daemon on AppleHV
(macOS) or an equivalent Linux runtime, and the gateway probes
require internet access. CI runs Ubuntu with no
podman runtime, so the probes are no-ops there. The structural
tests (the M2 argv + envelope gates, and the M4-T8 SSRF / bypass /
content corpora) *do* run in CI — they provide the structural
guarantee; the integration probes provide the behavioral
double-check on a developer's machine.

The reference run on 2026-08-30 (macOS 14 / podman 5.8.2 /
applehv) passed all five probes. See [`docs/demo-m2.md`](docs/demo-m2.md).

When FTS5 support is needed, add the build tag: `go build -tags sqlite_fts5 .`

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
athanor project create -name demo -archetype text -goal "..." [-criteria "a;b"]
athanor goal submit -project <id> -goal "..."
athanor job watch -job <id>            # prints each phase transition, then the artifact
athanor artifacts -project <id>
athanor freeze                         # §22 kill switch; frozen state survives restarts
athanor unfreeze -reason "..."         # requires a reason; recorded in the event log
```

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
