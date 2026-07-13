-- Git provider connections (tokens encrypted) and per-project webhook
-- secrets for auto-deploy.

CREATE TABLE git_connections (
    id         INTEGER PRIMARY KEY,
    provider   TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    name       TEXT NOT NULL UNIQUE,
    token_enc  BLOB NOT NULL,          -- AES-256-GCM
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

ALTER TABLE projects ADD COLUMN git_connection_id INTEGER REFERENCES git_connections(id);
ALTER TABLE projects ADD COLUMN webhook_secret_enc BLOB;
