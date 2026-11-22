-- Alert channels and the outbox that delivers to them (v0.63 U13 A1).
--
-- notification_channels holds where alerts go. config_enc is the channel's
-- settings as JSON, sealed with secrets.Box, because it carries webhook
-- secrets, bot URLs and SMTP passwords. events is a JSON array of event kinds
-- the channel takes ('[]' means every kind); project narrows it to one
-- project's events when set.
--
-- notifications is the outbox: one row per channel per event. A send that
-- fails is retried with backoff until max attempts, then marked 'failed'. A
-- failed send is never the deploy's or backup's failure; the row is the only
-- record of it. Payloads hold names, kinds and messages, never env values or
-- file contents.
--
-- Plain tables, no CHECK on status or kind, for the reason migration 0012
-- gives. An older binary ignores both tables.

CREATE TABLE notification_channels (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    config_enc  BLOB    NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    events      TEXT    NOT NULL DEFAULT '[]',
    project     TEXT,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE notifications (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id      INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event           TEXT    NOT NULL,
    project         TEXT,
    payload         TEXT    NOT NULL,
    status          TEXT    NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_error      TEXT,
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    sent_at         TEXT
);

CREATE INDEX notifications_due ON notifications (status, next_attempt_at);
CREATE INDEX notifications_channel ON notifications (channel_id, id);
