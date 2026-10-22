-- Personal API tokens.
--
-- A token lets a script, a CI job or an AI agent call /api/v1 as the user who
-- made it, without that user's password or second factor. It never carries
-- more than its user: the effective role is the lower of the user's current
-- role and the token's scope, worked out on every request, so demoting or
-- disabling the user caps or kills every token they hold at once.
--
-- Only a SHA-256 of the secret half is stored. The secret is 160 random bits,
-- so a slow password hash would buy nothing. The public id is the lookup key
-- and is safe to show in lists and logs.
--
-- Revoking sets revoked_at rather than deleting, so the audit log can still
-- name the token that did something.

CREATE TABLE api_tokens (
    id               INTEGER PRIMARY KEY,
    public_id        TEXT NOT NULL UNIQUE,
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    secret_hash      TEXT NOT NULL,          -- hex SHA-256 of the secret half
    scope            TEXT NOT NULL CHECK (scope IN ('read', 'write', 'admin')),
    -- Unmasked env reads. Off unless asked for: env values are the
    -- credentials of every app on the host.
    can_read_secrets INTEGER NOT NULL DEFAULT 0,
    expires_at       TEXT NOT NULL,
    last_used_at     TEXT,
    last_used_ip     TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at       TEXT
);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);

-- Which token, if any, performed an audited action. NULL for browser sessions.
ALTER TABLE audit_log ADD COLUMN token_id INTEGER REFERENCES api_tokens(id) ON DELETE SET NULL;
