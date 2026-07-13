-- Backups: project-dir archives (plus DB dumps for template databases),
-- stored locally and optionally uploaded to S3-compatible storage.

CREATE TABLE backups (
    id          INTEGER PRIMARY KEY,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL DEFAULT 'manual' CHECK (kind IN ('manual', 'scheduled')),
    destination TEXT NOT NULL DEFAULT 'local' CHECK (destination IN ('local', 's3')),
    path        TEXT NOT NULL DEFAULT '',   -- local path or S3 key
    size        INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
    error       TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT
);
CREATE INDEX idx_backups_project ON backups(project_id, id DESC);

CREATE TABLE backup_schedules (
    id              INTEGER PRIMARY KEY,
    project_id      INTEGER NOT NULL UNIQUE REFERENCES projects(id) ON DELETE CASCADE,
    interval        TEXT NOT NULL CHECK (interval IN ('hourly', 'daily', 'weekly')),
    destination     TEXT NOT NULL DEFAULT 'local' CHECK (destination IN ('local', 's3')),
    retention_count INTEGER NOT NULL DEFAULT 7,
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_run_at     TEXT
);
