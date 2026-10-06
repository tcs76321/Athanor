# ADR 0052 — Daydreaming engine

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §17
(actions, constraints, `DaydreamLog`), §13.4 (strategy mining), §18.1
(feedback review), §24 (battery gating), §28.1 (`daydream` events); ROADMAP
M7-T2

## Context

M5-T6/T8 shipped a minimal idle loop in `cmd/athanor/daydream.go` running two
of the six §17.1 actions (memory consolidation, repository exploration) and
auditing `daydream` events. M7-T2 must add the remaining actions, persist the
§17.3 `DaydreamLog`, and enforce the §17.2 constraints (yield, budget,
draft-only, no commits, disabled on battery). `skill_refinement` stays
deferred with the skills runtime (ROADMAP §7).

The loop must stay in `cmd/` because it needs the MCE summarizer, the
repository indexer, and an LLM adapter — all cmd-side (ADR-0021 §2 keeps
`internal/mce` free of `internal/llm`).

## Decision

Add `internal/daydream` for the persisted record only, and extend the cmd
driver:

- **`internal/daydream`** owns the closed §17.1 action set and the
  `DaydreamLog` model + `Repo` over migration 0022. It cannot promote
  anything: outputs are recorded, never accepted.
- **Three new actions** in the driver:
  - *Proactive Documentation* (`main`): for the first repository-backed
    project with no document artifact, generate a bounded draft README and
    persist it as a **draft**. Idempotent — the artifact's existence makes the
    next pass skip the project. The LLM access is a `daydreamGenerator` seam
    (`cmd/athanor/daydream_generator.go`), so tests inject a fake.
  - *Feedback Review* (`security`, no inference): a project-scoped
    `CorrectionRecord` rule recurring across ≥2 projects is proposed once as a
    global correction. A new `corrections.SourceFeedbackReview` maps to a
    global default; an existing global rule suppresses re-proposal.
  - *Strategy Mining* (`security`, no inference): calls the deterministic
    §13.4 `strategy.Mine` with the configured cohort floor and delta; `Mine`
    persists proposed insights and invents no numbers.
- **Constraints.** `pass` keeps the config/power/kill-switch/idle gates and
  the shared §17.2 wall-time budget; battery is enforced by the M7-T1 power
  profile (`AllowDaydreaming=false` on battery). Actions run sequentially, so
  the max-2-concurrent bound is never exceeded. Nothing commits or writes to
  the workspace outside a draft artifact.
- Each action records one `DaydreamLog` row (nil-safe) in addition to its
  existing `daydream` audit event.

## Consequences

- All five in-scope §17.1 actions run, and every one is observable through
  `daydream_logs` (`Recent`) plus the append-only event log.
- Proactive Documentation is deliberately shallow (a README draft, not
  per-symbol docstring analysis); deepening it is a future task with the
  skills runtime.
- `SourceFeedbackReview` extends the §18.1 source set with a machine source;
  the correction is machine-proposed and low severity by default, promoting
  only on operator review like every other correction.
