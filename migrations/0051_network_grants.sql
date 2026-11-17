-- 0051_network_grants: project-scoped, time-bounded egress grants.
--
-- The global policy is the ceiling; a grant is an operator-approved URL base
-- for one project. It never overrides a global deny and never disables the
-- private/metadata guard. Expiry is evaluated at query time (expires_at <= now)
-- and revocation is explicit, so a grant can be audited without deleting it.

CREATE TABLE IF NOT EXISTS network_grants (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    scheme      TEXT NOT NULL CHECK (scheme IN ('http','https')),
    host        TEXT NOT NULL,
    path_prefix TEXT NOT NULL DEFAULT '/',
    reason      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at  TEXT,
    revoked_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_network_grants_project
    ON network_grants(project_id, status);
