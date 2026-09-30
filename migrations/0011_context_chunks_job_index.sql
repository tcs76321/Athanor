-- Migration 0011: Dormant Index lookup by job (ROADMAP M5-T5; ADR-0023 §7).
--
-- The M5-T5 assembler publishes the §10.1 Dormant Index for the job it is
-- working on (§11.2 §10), so the model can request a dormant chunk by ID.
-- `context_chunks` already carries project_id and job_id (migration 0009)
-- and is indexed on source_hash and project_id only; without a job index
-- the per-call lookup degrades to a table scan as the chunk store grows.
--
-- Index-only and forward-only: no table or column changes, so existing
-- rows, ids, and hashes are untouched.

CREATE INDEX idx_context_chunks_job ON context_chunks(job_id);