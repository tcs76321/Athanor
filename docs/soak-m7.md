# M7-T9 — sustained-load soak runbook

This is the **human-run** endurance checkpoint for Gate G7. The preferred
harness is the M8-T23 `m3-t7-probe soak` subcommand (one long-lived daemon);
`scripts/soak.sh` (`make soak`) is the legacy per-iteration runner. The run is
bounded by a wall-clock deadline (`-until`), not a fixed duration, and is
resumable and rolling so a short or interrupted run still yields a readout.
Acceptance is by coverage ([ADR-0066](adr/0066-release-scope-soak.md)).

## Prerequisites

- AC power (the run must not pause on battery; §24).
- A running Ollama with the persona models pulled, and
  `OLLAMA_MAX_LOADED_MODELS=1`, `OLLAMA_NUM_PARALLEL=1` (§12.5). See
  [`../DEVELOPMENT.md`](../DEVELOPMENT.md#model-residency).
- Podman rootless available and the Job Pod image built
  (`make jobpod-image`) with `job_pod.image` set.
- `make build` (the probe starts the daemon itself).
- Phase budgets sized for the chosen model. The `soak` subcommand **generates
  its own `config.yaml`** under the output dir (900 s phase budgets, see
  `probeconfig.go`); the legacy `scripts/soak.sh` uses the checked-in config.

## Preflight (verify before the run)

1. **Single model residency.** `OLLAMA_MAX_LOADED_MODELS=1` and
   `OLLAMA_NUM_PARALLEL=1` must be set on the Ollama **server** (restart Ollama
   after setting them). The Core can only *verify* this, not set it; the probe
   aborts with "model residency violated" when two models are resident. The M9
   qualification run aborted on exactly this — see
   [`soak-m9-findings.md`](soak-m9-findings.md).
2. **Job Pod image.** `podman images | grep python:3.12-alpine` (the generated
   config sets `job_pod.image: python:3.12-alpine`). Pull with
   `make integration-images`.
3. **Binary current.** `make build` (the probe starts `bin/athanor` itself).
4. **Disk.** A few GB free: the run writes a result tree plus state per pass.
5. **AC power**, and plan the one human sleep/wake cycle.

## Recommended 8-hour run

```bash
make build
make integration-images        # once, only if python:3.12-alpine is absent
go run -tags sqlite_fts5 ./spikes/m3-t7-probe soak \
  -model gemma12b -arm full \
  -corpus eval/bench/tasks.yaml \
  -judge-model granite4.2:3b \
  -hours 8 -kill-after 2h \
  -out spikes/m3-t7-probe/results/soak-0.0.0
```

- `-model gemma12b` is the routed `main` (best generator on the H-tier code
  tasks; [qualification smoke](qualification-smoke-m9.md)); `-judge-model
  granite4.2:3b` is the best cross-family (`security`) judge.
- `-corpus eval/bench/tasks.yaml` is the 15-task bench set. Non-code fixtures
  are indexed as the project repository (M9), so a "provided source" task
  actually sees its source; without that, those tasks can only fail.
- `-kill-after 2h` injects one SIGKILL + restart to exercise crash recovery.
- Leave `-gates` **off** for a clean capability readout: the F5
  `require_documentation_for_code` gate rejects valid code whose corpus criteria
  omit docstrings, confounding the quality numbers. Run a separate `-gates` arm
  for production-faithful acceptance.

Do the sleep/wake cycle by hand mid-run and record it (the harness cannot sleep
the host safely).

## Mini validation run (1 hour)

Before committing to a long run, validate the harness end to end in an hour.
The `soak` subcommand drives the full arm over the bench corpus, samples
endurance, injects one crash, and also reduces the event log (telemetry), writes
the per-model leaderboard, and runs the conformance scenarios — all offline:

```bash
make build
make soak-mini            # ornith9b, full arm, 1h, one kill -9 at 20m
```

which is:

```bash
go run -tags sqlite_fts5 ./spikes/m3-t7-probe soak \
  -model ornith9b -arm full -corpus eval/bench/tasks.yaml \
  -judge-model granite4.2:3b -hours 1 -kill-after 20m \
  -out spikes/m3-t7-probe/results/mini-soak
```

Override `MINI_SOAK_MODEL`, `MINI_SOAK_JUDGE`, `MINI_SOAK_HOURS`,
`MINI_SOAK_KILL`, or `MINI_SOAK_OUT`. Use a **fresh** output dir for a clean
run; a reused one resumes from its persisted deadline and metrics. Read the
rolling `report.md` (written after every pass): an hour rarely completes a full
pass over all 15 tasks at the `full` arm, so a partial readout is expected, and
`push-local-remote` reports PASS (or SKIP when no artifact was committed), never
FAIL.

## Run

```bash
make build
# deadline mode (preferred): run until tonight's local midnight, then stop
go run ./spikes/m3-t7-probe soak \
  -model ornith9b -corpus eval/bench/tasks.yaml -until midnight \
  -kill-after 2h -gates -judge-model granite4.2:3b \
  -out spikes/m3-t7-probe/results/soak-0.0.0

# fixed-duration variant
go run ./spikes/m3-t7-probe soak -model ornith9b -corpus eval/bench/tasks.yaml -hours 4
```

The `soak` subcommand holds **one long-lived daemon** over the bench corpus.
`-until` accepts `midnight` (the upcoming local 00:00), `HH:MM` (the next
occurrence), or an RFC3339 timestamp; it overrides `-hours`, and the resolved
deadline is persisted to `<out>/deadline` so a resume does not slide it.

- Results accumulate in `<out>/metrics.jsonl` (append-only); re-running resumes
  without resetting.
- A rolling `report.md` / `results.json` / `results.csv` is written after every
  pass, so stopping early still yields a readout for what completed.
- `SIGINT`/`SIGTERM` finalizes cleanly (flush + report + stop daemon).
- `-kill-after` injects one `kill -9` + restart, exercising crash recovery; the
  event lands in `<out>/soak-events.log`.
- `<out>/soak.csv` samples RSS, SQLite size, free disk **and the live goroutine
  count** (read from `/healthz`, M8-T31) every `-soak-interval`.
- The soak config **compresses the time-triggered schedules** (backup every
  15m, HITL TTL 5m) so a deadline run still exercises them; the production
  `0 3 * * *` spec stays pinned by `internal/backup`'s cron test.
- The sleep/wake checkpoint remains a human step (the host cannot be slept
  safely by the harness); record it alongside the run.

`scripts/soak.sh` (`make soak`) is the **legacy** per-iteration runner; prefer
the `soak` subcommand.

## Conformance scenarios (ADR-0073)

At the end of a run the harness drives a small set of end-to-end scenarios
against the live daemon and folds a pass/fail matrix into `report.md`:

- **freeze-unfreeze** — the §22 kill switch: freeze, a reasonless unfreeze is
  rejected, a reasoned unfreeze clears it.
- **ui-pages** — the dashboard, statistics, projects, and corrections pages
  render.
- **ui-sse** — the dashboard event stream opens (`text/event-stream`).
- **push-local-remote** — an approved HITL push delivers the agent branch to a
  **local bare repository** (a real push, no network).
- **backup-restore** — the §23.4 cycle on the run's own state dir: snapshot,
  restore into a scratch directory, read the database back.

The set is offline (loopback only) and grows only when a scenario proves
something no existing gate already proves. A run with no accepted repo-backed
artifact reports `push-local-remote` as skipped, not failed.

## Model comparison (benchmark readout)

One `soak` invocation is one model, so single-model residency is preserved
across a comparison ([ADR-0072](adr/0072-single-slot-execution.md)). To compare
the ladder, run the soak once per model into a subdirectory of a common tree,
then aggregate the whole tree:

```bash
for m in granite3b gemmae4b granite8b ornith9b gemma12b \
         qwen27b granite30b nemotron30b muse30b ornith35b; do
  go run -tags sqlite_fts5 ./spikes/m3-t7-probe soak \
    -model "$m" -arm full -corpus eval/bench/tasks.yaml \
    -judge-model granite4.2:3b -hours 8 \
    -out spikes/m3-t7-probe/results/ladder/"$m"
done
go run -tags sqlite_fts5 ./spikes/m3-t7-probe report -out spikes/m3-t7-probe/results/ladder
```

`report` walks the tree and merges every `results.json`. The rolling
`report.md` gains a **Model leaderboard** (grounded pass rate, tokens per
passing job, wall-time p50/p95) and **Telemetry** tables (per-model calls and
tokens, and calls by phase) reduced from each job's event log
([ADR-0073](adr/0073-soak-validation-harness.md) §3) — the model-comparison and
engine-attribution data the soak previously discarded. Then `probe-measure` +
`athanor models -qualification` turn the same tree into a routing proposal.

## Acceptance (Gate G7)

Coverage-based ([ADR-0066](adr/0066-release-scope-soak.md)):

- Zero orphan Job Pods after the run (`podman ps -a`) and after a `kill -9`
  mid-run.
- No unrecovered crashes: a process killed mid-run is recovered on restart
  (§23.6), or the failure is captured with a reason in the event log.
- Bounded growth: RSS **and goroutine count** do not grow monotonically across
  the run (inspect `soak.csv`); SQLite/WAL size plateaus.
- The time-triggered arms fired: at least one scheduled backup and one HITL
  expiry appear in the event log / `<out>/soak-events.log`.
- The system survives at least one sleep/wake cycle mid-run (macOS/lab):
  jobs pause and resume per §24.

Record the outcome (date, hardware, Ollama version, model, pass/fail per
item, `soak.csv` location) alongside the results. This runbook and the
aggregated `report.md` are the evidence for the Gate G7 soak arm.

## After the run

```bash
make probe-report  PROBE_OUT=spikes/m3-t7-probe/results/soak-0.0.0
make probe-measure PROBE_OUT=spikes/m3-t7-probe/results/soak-0.0.0
./bin/athanor models -qualification spikes/m3-t7-probe/results/soak-0.0.0/measurements.json
```

`probe-report` re-aggregates the rolling `report.md`; `probe-measure` reduces
the run into `onboard.Measurement` JSON (with `Samples`); `athanor models`
turns that into an advisory routing proposal. The soak writes a rolling
`report.md`/`results.json`/`results.csv` after every pass, so stopping early
still yields a readout.
