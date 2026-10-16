-- known-issues M1: multi-owner chunk attribution.
--
-- Chunk ids are content-derived (ADR-0021 §5), so identical bytes divided for
-- two projects resolve to the same chunk id. The single (project_id, job_id)
-- columns on context_chunks could only record one owner, so re-ingesting the
-- same content under a second scope kept the first writer's attribution and
-- the second scope's index was incomplete. This table records every
-- (chunk, scope) pair. The context_chunks columns are retained (informational,
-- last writer wins) but are no longer authoritative for ownership reads.
CREATE TABLE IF NOT EXISTS context_chunk_owners (
    chunk_id   TEXT NOT NULL REFERENCES context_chunks(id),
    project_id TEXT NOT NULL DEFAULT '',
    job_id     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (chunk_id, project_id, job_id)
);

-- IndexForProject looks up (project_id, job_id=''); IndexForJob looks up
-- (job_id != '').
CREATE INDEX IF NOT EXISTS idx_context_chunk_owners_project
    ON context_chunk_owners(project_id) WHERE job_id = '';
CREATE INDEX IF NOT EXISTS idx_context_chunk_owners_job
    ON context_chunk_owners(job_id) WHERE job_id != '';

-- Backfill every existing attribution.
INSERT OR IGNORE INTO context_chunk_owners (chunk_id, project_id, job_id)
    SELECT id, COALESCE(project_id, ''), COALESCE(job_id, '')
    FROM context_chunks
    WHERE project_id IS NOT NULL OR job_id IS NOT NULL;
