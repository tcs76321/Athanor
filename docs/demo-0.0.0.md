# 0.0.0 release demo and gate checklist

**Status:** open · **Owner:** human checkpoint · **Runtime:** Core Pod only
**Refs:** [0.0.0 plan](0.0.0-plan.md) §2c/§4, [ADR-0136](adr/0136-v5-grounded-agency-benchmark.md),
[security audit](security-audit-0.0.0.md).

This is the human sign-off sheet for the 0.0.0 tag. A row is checked only when
the named step was run against the **Core Pod**, not the host development
binary. Any unchecked row blocks the tag.

## GUI day-loop (no CLI)

Run on a fresh install, in the browser, on the Core Pod. Record the evidence
(screenshot, log, or artifact id) beside each step.

| # | Step | Pass | Evidence |
|---|---|---|---|
| 1 | Install reaches the UI; the header names the Core Pod and version | [ ] | |
| 2 | Onboarding runs the environment checks and a bounded qualification; the routing table is reviewable and applies | [ ] | |
| 3 | Create a project from one goal; approve a plan/direction; the first job starts | [ ] | |
| 4 | Watch the job: phases, tool calls, tokens, evaluations, final artifact; pause/cancel/retry and leave a note | [ ] | |
| 5 | Review accepted work: what was built, how to run, cost, known-broken; record a blinded rating | [ ] | |
| 6 | Morning digest and review queue show the work and the next decision | [ ] | |
| 7 | Inspector shows events, model calls, checks, memory entries, and runtime settings; payload capture is opt-in | [ ] | |
| 8 | The whole loop ran against the Core Pod, not a host daemon | [ ] | |

## Benchmark freeze

| Gate | Command / evidence | Pass |
|---|---|---|
| One full 2×2 at one commit, clean tree | `mixed_revisions: false` in both reports | [ ] |
| Corpus and protocol digests match | `make corpus-check-all` | [ ] |
| Report matches committed rows; no ghost claims | `make bench-v5-verify` (strict) | [ ] |
| Safety 0 failures | protocol report | [ ] |
| Utility component gates: briefs 2/2, tuning news 2/2, held-out day2 1/1, completion 5/5 | protocol report | [ ] |
| Memory correction/insight injection | memory report | [ ] |
| Mechanism coverage 1.0 | mechanism report | [ ] |
| Foundation non-inferiority (full ≥ single − 10) | paired report | [ ] |
| Quality established or explicitly `not_measured` | quality section + ADR | [ ] |
| `status: frozen`, `cut: 0.0.0`, `eval/bench/CURRENT` points at the frozen instrument | protocol + `CURRENT` | [ ] |

## Existing release gates

| Gate | Evidence | Pass |
|---|---|---|
| §31.3 security audit signed off, including Core Pod behavioral probes, gateway probes from inside the image, and the 24h soak | `security-audit-0.0.0.md` | [ ] |
| Fresh install < 15 min on Linux and macOS | timed demo | [ ] |
| Core Pod smoke | `make core-smoke` | [ ] |
| Backup/restore drill | `internal/backup` test + manual restore | [ ] |
| `make check`, `make cover-check`, `make test-integration` green | CI + local run | [ ] |

## Tag

- [ ] `version` stamped `0.0.0` in the binary and Core Pod image
- [ ] `CHANGELOG` `Unreleased → 0.0.0`
- [ ] `ROADMAP`/`README`/`ARCHITECTURE §32` match reality
- [ ] `git tag v0.0.0` from the release branch
- [ ] No scope creep after the tag
