-- 0052_coverage_items: what a recurring cycle has already covered.
--
-- The retain block records a structured item list; the next cycle reads the
-- recent keys and injects them as "do not present these as new" (the generic
-- no-repeat contract). A namespace separates independent ledgers (a daily
-- news wire vs a topic brief) inside one project.

CREATE TABLE IF NOT EXISTS coverage_items (
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    namespace     TEXT NOT NULL,
    item_key      TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    sources_json  TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (project_id, namespace, item_key)
);

CREATE INDEX IF NOT EXISTS idx_coverage_recent
    ON coverage_items(project_id, namespace, last_seen_at);
