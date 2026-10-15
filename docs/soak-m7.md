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
make soak                     # SOAK_HOURS=24 by default
SOAK_HOURS=1 make soak        # a smoke-length variant
SOAK_OUT=/tmp/soak make soak  # override the results directory
```

`scripts/soak.sh` repeats the quality-probe workload; each iteration writes
its packets and `soak.csv` under `$SOAK_OUT/iter-N/`, and the script runs a
final `report`. The probe runner manages the daemon lifecycle and enforces
per-job wall-time, disk, and consecutive-error budgets, so a wedged iteration
aborts rather than hanging the soak.

## Combined endurance + quality soak (M8-T23)

The probe adds a `soak` subcommand: one long-lived daemon over the bench
corpus, resumable and early-stoppable.

```bash
go run ./spikes/m3-t7-probe soak \
  -model ornith9b -corpus eval/bench/tasks.yaml -hours 24 -kill-after 6h \
  -out spikes/m3-t7-probe/results/soak
```

- Results accumulate in `<out>/metrics.jsonl` (append-only); re-running resumes
  without resetting.
- A rolling `report.md` / `results.json` / `results.csv` is written after every
  pass, so stopping early still yields a readout for what completed.
- `SIGINT`/`SIGTERM` finalizes cleanly (flush + report + stop daemon).
- `-kill-after` injects one `kill -9` + restart, exercising crash recovery; the
  event lands in `<out>/soak-events.log`.
- The sleep/wake checkpoint remains a human step (the host cannot be slept
  safely by the harness); record it alongside the run.

## Acceptance (Gate G7)

- Zero orphan Job Pods after the run (`podman ps -a`) and after a `kill -9`
  mid-run.
- No unrecovered crashes: every iteration that dies is recovered on restart
  (§23.6), or the failure is captured with a reason in the event log.
- Memory and goroutine counts bounded: RSS does not grow monotonically across
  iterations (inspect `soak.csv`).
- Disk usage stable: WAL growth bounded; SQLite file size plateaus after
  checkpointing.
- The system survives at least one sleep/wake cycle mid-run (macOS/lab):
  jobs pause and resume per §24.

Record the outcome (date, hardware, Ollama version, model, pass/fail per
item, `soak.csv` location) alongside the results. This runbook and the
aggregated `report.md` are the evidence for the Gate G7 soak arm.
