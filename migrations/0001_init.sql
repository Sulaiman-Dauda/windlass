-- Windlass metadata schema. SQLite, WAL mode, foreign_keys=ON.
-- Metadata only: application state lives in Docker/Compose/disk (principle 2).

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT,                -- argon2id encoded; NULL for OAuth-only accounts
    role          TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member', 'viewer')),
    totp_secret_enc BLOB,              -- AES-256-GCM, nonce-prefixed
    totp_enabled  INTEGER NOT NULL DEFAULT 0,
    oauth_provider TEXT,               -- 'github' | 'google'
    oauth_subject TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    disabled_at   TEXT,
    UNIQUE (oauth_provider, oauth_subject)
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,       -- SHA-256 hash of the opaque session token
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at TEXT NOT NULL,
    ip         TEXT,
    user_agent TEXT
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE audit_log (
    id            INTEGER PRIMARY KEY,
    ts            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    user_id       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    action        TEXT NOT NULL,       -- e.g. 'project.deploy', 'auth.login'
    resource_type TEXT,
    resource_id   TEXT,
    ip            TEXT,
    detail        TEXT                 -- JSON
);
CREATE INDEX idx_audit_ts ON audit_log(ts);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL                -- JSON
);
