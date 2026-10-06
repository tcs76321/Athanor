# ADR 0051 — Power/idle supervisor

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §24,
§28.1 (`power` events), §17.2 (no daydreaming on battery); ROADMAP M7-T1;
[ADR-0050](0050-doctor.md) (the pure-package + cmd-adapter + Gate G1 pattern)

## Context

`internal/power` shipped with profiles (`autonomous` / `interactive`), a
`Limits` value (concurrency cap, daydreaming allowance), and an `OSWatcher`
stub — but nothing read the machine. The power profile stayed at its
conservative `interactive` default forever, so Daydreaming (M5-T6) never ran
and §24's battery/sleep/suspend rules were inert. M7-T1 must make the profile
track AC/battery, idleness, active hours, sleep/wake, and pause deep work on
battery.

Two constraints:

1. **Gate G1.** The OS reads (`pmset`/`ioreg`, `/sys/class/power_supply`,
   `xprintidle`, `caffeinate`, `systemd-inhibit`) use `os/exec`, which
   `internal/` may not import.
2. **Testability.** Every §24 row must be a deterministic test, not a
   property of the developer's laptop.

## Decision

Split the feature three ways:

1. **Pure policy** (`internal/power/policy.go`). `Decide(Observation, Config)
   Decision` resolves the §24 table with no clock, lock, or OS call. Sleep
   and battery pause first; then active hours and idleness choose the
   profile. `withinActiveHours` handles overnight windows and the all-day
   `00:00-24:00`, failing open on a malformed spec (config validation
   rejects those upstream).
2. **Supervisor** (`internal/power/supervisor.go`). Owns the clock and the
   injected `Observer` interface. Each tick: observe → decide →
   `PowerManager.ApplyDecision` → hold/release the `OSWatcher` assertion →
   fire `OnPause`/`OnResume` hooks. A poll gap larger than 3× the interval is
   treated as a wake event. The loop has no OS calls, so tests drive `Tick`
   directly.
3. **Platform adapters** (`cmd/athanor/power_{darwin,linux,other}.go`). The
   Darwin observer reads `pmset -g batt` + `ioreg`; Linux reads
   `/sys/class/power_supply` and `xprintidle` (a headless host counts as
   idle). The assertions are `caffeinate -i -w <pid>` (Darwin) and
   `systemd-inhibit --what=idle:sleep` (Linux). Both files join
   `allowedOsExecFiles` in the Gate G1 allowlist.

**Engine.** A new `PauseGate` seam (`Paused() bool`, satisfied by
`*power.PowerManager`) joins the kill switch in the run loop's single stop
gate; `pauseReason()` reports `kill_switch` or `power`. `ResumePaused`
transitions each `paused` job back to its `paused_from` state and re-enqueues
it. The power pause is **separate from the kill switch**: it is transient and
clears without an operator reason, whereas freeze persists and requires
`unfreeze -reason`.

## Consequences

- The §24 table is a table test (`internal/power/policy_test.go`), and the
  transition machinery is tested with a scripted observer and a counting
  watcher (`supervisor_test.go`).
- Daydreaming now activates only on AC + idle within active hours, and the
  engine's concurrency cap follows the profile live (already wired in
  M1-T8.4).
- **Limitations (documented, not hidden):** sleep is detected indirectly via
  the poll-gap heuristic, not an OS event, so a wake is noticed on the next
  tick; a job paused by the gate and surviving a daemon restart stays paused
  until the gate reopens (the boot path is `Recover`, which does not resume
  operator- or power-paused jobs). Both are candidates for a future
  event-driven macOS/Linux adapter (IOKit / logind D-Bus) if demand appears.
