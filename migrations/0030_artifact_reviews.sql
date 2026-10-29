-- F4 first-use review queue.
--
-- One row per human review of an accepted artifact. The review is stored with
-- provenance (artifact/job/project, reviewer, rubric, optional note) so a later
-- analysis can join it to the run without trusting a free-text label. The
-- queue itself is a query (accepted artifacts with no review), not a table.
CREATE TABLE IF NOT EXISTS artifact_reviews (
    id              TEXT PRIMARY KEY,
    artifact_id     TEXT NOT NULL REFERENCES artifacts(id),
    job_id          TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    reviewer        TEXT NOT NULL DEFAULT 'operator',
    score           REAL NOT NULL DEFAULT 0.0
                    CHECK (score >= 0.0 AND score <= 1.0),
    accepted        INTEGER NOT NULL DEFAULT 0,
    blinded         INTEGER NOT NULL DEFAULT 1,
    rubric_json     TEXT NOT NULL DEFAULT '{}',
    note            TEXT NOT NULL DEFAULT '',
    provenance_json TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_artifact_reviews_artifact
    ON artifact_reviews(artifact_id, created_at);
CREATE INDEX IF NOT EXISTS idx_artifact_reviews_project
    ON artifact_reviews(project_id, created_at);
