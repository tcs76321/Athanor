# Athanor Host Adapter — source material

Each section is identified by its bracketed id. Cite the id of the section a
claim comes from.

## [S1] Purpose

The Host Adapter is the minimal bridge between the operating system and the
Core Pod. It owns exactly three responsibilities: power observation, filesystem
events, and Podman lifecycle. It contains no business logic and no model calls.

## [S2] Power observation

On macOS the adapter reads AC/battery state via `pmset -g batt` and idle time
via `ioreg`. On Linux it reads `/sys/class/power_supply` and `xprintidle`. It
publishes a profile (`interactive`, `deferential`, `autonomous`) and never
pauses work itself; pausing is the Core's decision.

## [S3] Filesystem events

The adapter watches the workspace `inbox/` with `fsnotify` and forwards create
and rename events to the Core's ingress pipeline. It does not read file
contents; scanning is the Core's job.

## [S4] Podman lifecycle

On macOS, rootless Podman runs inside a VM (`podman machine`). The adapter
starts and stops that machine and accounts for the VM's memory overhead when
the Core budgets context windows.

## [S5] Failure handling

Every adapter failure is non-fatal to the Core: a missing `pmset` degrades to
"AC assumed", a missing `fsnotify` degrades to no live ingress events. The
adapter never blocks the Core on a host facility.

## [S6] Non-goals

The adapter does not expose a network port, does not store state, and does not
know about projects, tasks, or artifacts.
