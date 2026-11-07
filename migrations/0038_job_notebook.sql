-- 0038_job_notebook: persistent per-job working memory (ADR-0109).
--
-- The notebook is the explicit state the model carries across phases: the
-- plan it formed, candidates it tried, results it observed, and decisions it
-- made. It is append-only by convention (the engine only appends) and bounded
-- per job by the store, so a long job cannot grow it without limit.
--
-- The content is prompt material, not a replacement for the event log or
-- artifact bytes. Unlike the event log it is intentionally compact and
-- model-readable.

CREATE TABLE job_notebook (
    id         TEXT PRIMARY KEY,
    job_id     TEXT NOT NULL,
    project_id TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN (
        'plan','hypothesis','observation','experiment','result','decision',
        'question','answer'
    )),
    phase      TEXT NOT NULL DEFAULT '',
    content    TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX idx_job_notebook_job ON job_notebook(job_id, created_at, id);
CREATE INDEX idx_job_notebook_project ON job_notebook(project_id, created_at);
