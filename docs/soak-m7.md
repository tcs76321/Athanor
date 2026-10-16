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
- A real `config.yaml` (or `config-probe.yaml`) with phase budgets sized for
  the chosen model.

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
