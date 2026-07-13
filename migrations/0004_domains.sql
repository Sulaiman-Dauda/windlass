-- Domains route a hostname through Caddy to a compose service's container
-- port. Caddy owns TLS (automatic HTTPS); Windlass owns only its tagged
-- route subtree in Caddy's config.

CREATE TABLE domains (
    id             INTEGER PRIMARY KEY,
    project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    hostname       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    service        TEXT NOT NULL,
    container_port INTEGER NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_domains_project ON domains(project_id);
