# ADR 0040 — Strategy capture (M6-T10)

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §13.3,
§13.4, §8.3; ROADMAP M6-T10; ADR-0039 (local web UI).

## Context

§13.3 records the strategy every job exercises — which personas ran which
phases at what temperatures, with how many candidates — so outcomes can
accumulate into statistical evidence (§13.4, mined in M6-T11). The `jobs`
table has carried an unused `strategy_profile_json` column since migration
0001, but there is no `StrategyProfile`/`StrategyOutcome` store and nothing
captures either record.

## Decision

### 1. Migration 0021 creates the three tables

`strategy_profiles` (one per job, `UNIQUE(job_id)`), `strategy_outcomes`
(one per job, linked to its profile, `CHECK`ed result enum), and
`strategy_insights` (created here, mined in M6-T11). The unique constraints
make capture idempotent, which is what lets recovery and backfill re-run
safely.

### 2. `internal/strategy` owns the store

`CreateProfile`/`GetProfileByJob` and `CreateOutcome`/`GetOutcomeByJob`
round-trip the §13.3 shapes; `ListOutcomes` feeds M6-T11.
`CreateOutcome` resolves the profile by job ID and rejects an outcome with no
profile, so the two records can never diverge.

### 3. The engine derives both records with zero inference

- **Profile, at start.** `Engine.SetStrategySink` is a nil-safe seam.
  `captureProfile` runs when a job is queued and builds the signature from
  the engine's fixed phase → persona map (`tall`/`main`/`security`) with the
  resolved §13.1 temperatures and the configured divergence candidate count.
  No model call is involved.
- **Outcome, at end.** `captureOutcome` runs at a terminal state and derives:
  `result` from the job state plus the last `comparison` audit row's winner;
  `score` from the job's evaluation records; `evaluator_confidence` and
  `token_cost` from its `llm_call`/`comparison` events; `wall_time` from the
  job's timestamps; `reflection_loops` from the typed counter. Nothing is
  inferred by a model.

`task_type`/`difficulty_hint` are left empty: the engine has no structured
planner output to populate them without inference, so §13.3's "zero
inference" rule wins over completeness.

### 4. Capture is adjacent to transitions, not inside their transaction

§13.3 says the records are written transactionally with the state
transitions. The transition transaction belongs to `job.Repository`, and
threading an arbitrary write into it would widen that package's contract for
one consumer. Capture is therefore idempotent and derived, and is called
immediately after the relevant transition (start and terminal); a crash in
the gap is healed by `Recover` re-running the job (which re-captures) or by
`Backfill`. The acceptance intent — one durable profile and one immutable
outcome per completed job, no inference — holds; the literal same-transaction
guarantee is traded for a narrow job-repository contract.

### 5. Boot backfill

`Repo.Backfill` runs at boot: for terminal jobs with no profile it creates a
default profile (`archetype: legacy`, empty signature) and an outcome derived
from the job state. It is bounded to jobs missing a profile and idempotent.

## Consequences

- Every completed (and failed/cancelled) job now has both records; M6-T11 can
  aggregate outcomes without another capture path.
- The records are small, immutable, and retained indefinitely.
- No new dependency; Gate G1 is unaffected.

## Implemented (M6-T10)

Migration 0021; `internal/strategy` (profile/outcome round-trip, idempotent
capture, `Backfill`, `ListOutcomes`) with tests; the engine `StrategySink`
seam wired at boot; and boot backfill. `strategy_insights` is created but
unused until M6-T11.
