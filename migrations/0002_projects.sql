-- Projects are directories on disk; this table holds only metadata
-- (principle 1: no compose content in the database — disk is truth).

CREATE TABLE projects (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    source      TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('git', 'manual', 'template')),
    git_repo    TEXT,
    git_branch  TEXT,
    auto_deploy INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE env_vars (
    id         INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value_enc  BLOB NOT NULL,          -- AES-256-GCM, nonce-prefixed
    UNIQUE (project_id, key)
);
CREATE INDEX idx_env_vars_project ON env_vars(project_id);
