# ADR 0114 — Deterministic job-summary memory

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §10.2, §10.5; ADR-0025 (compaction), ADR-0026 (retrieval),
ADR-0109 (notebook); `internal/mce`, `internal/engine`

## Context

The MCE has a retrieval path into tier 5, but the post-P0 full v2 run reported
**zero memory hits**. Part of the reason is the benchmark's project lifecycle;
the deeper design gap is that a completed job leaves no compact, retrievable
record of what it learned. Artifacts are versioned and the event log is
complete, but neither is a small semantic memo the next job can find by
similarity. The notebook is per-job, so it dies with its job.

The system already has a content-addressed `compacted_memory` table with an
FTS index and project-scoped retrieval. What it lacks is a producer for the
small, deterministic summary that a job can leave behind.

## Decision

On terminal **completed** jobs, write a bounded, deterministic **job summary**
memo into `compacted_memory`.

- **Producer.** `mce.CompactStore.RememberJobSummary(projectID, jobID,
  summary)` inserts a `deterministic` memo with
  `profile_type=job_summary`, `template_version=job-summary-v1`,
  `persona=engine`, temperature 0.0. It is not an LLM compaction; it copies
  the already-derived notebook/project/task summary. The `(kind,input_hash)`
  unique key makes the write idempotent across recovery and duplicate
  finalization.
- **Content.** The engine builds `Project / Task / Goal` plus the notebook's
  recent lines, capped at 4 KiB. It contains no artifact bytes, no secrets,
  and no raw test output beyond what the notebook already bounded.
- **Scope.** The memo carries `project_id` and `job_id`; retrieval remains
  project-scoped, so a memo cannot leak into another project. The FTS trigger
  syncs it automatically.
- **When.** Only completed jobs write a memo. Failed/cancelled jobs already
  produce CorrectionRecords; a "what not to do" memo is a later, separate
  producer. A memory write failure is logged and never changes the job
  outcome.
- **Audit.** A successful insert emits `memory_remembered` (context category);
  a failure emits `memory_remember_failed`.

## Consequences

- A project's later jobs can retrieve "what the previous job decided" from
  tier 5 instead of starting cold. This is the first producer that can make
  the documented retrieval path observable in real use.
- The memo is deterministic and content-addressed, so replaying a job does not
  duplicate memory and a changed summary is a new memo.
- The table grows by one small row per completed job. The existing MCE prune
  path and backup/retention policy apply; a future retention cap is a
  configuration decision, not a new table.
- This does not yet write model-authored hypotheses or cross-project lessons.
  Those are follow-ups on the same `RememberJobSummary` shape.

## Alternatives rejected

- **Use an LLM compaction for every job summary.** It costs a model call and
  can paraphrase away the exact decisions; the notebook is already the
  compact source. Deterministic copy is cheaper and more faithful.
- **Store the summary only in the event log.** It is not FTS-indexed and is
  not scoped/ranked for retrieval.
- **Write on every terminal state.** Failures are already captured as
  corrections; a summary of a failed attempt is a different, noisier signal.
