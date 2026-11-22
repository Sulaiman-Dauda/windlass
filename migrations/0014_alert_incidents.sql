-- Open alert incidents (v0.63 U13 A2).
--
-- One row per condition that is wrong now: a failed deploy, a full disk, a
-- container in a restart loop. The row is what makes an alert fire once per
-- incident, send one recovery message when it clears, and re-alert at most
-- once per re-alert period, across restarts of the panel. The key names the
-- subject inside the kind and project (a container, a domain), so two
-- unhealthy containers in one project are two incidents.
--
-- first_seen_at starts the debounce; alerted_at is unset until the alert
-- went to the outbox, so a condition that clears inside the debounce sends
-- nothing. seen_at is when a sweep or event last confirmed it.
--
-- Rebuildable state: dropping every row only means open incidents alert
-- again. An older binary ignores the table.

CREATE TABLE alert_incidents (
    key           TEXT PRIMARY KEY,
    kind          TEXT NOT NULL,
    project       TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL,
    message       TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    seen_at       TEXT NOT NULL,
    alerted_at    TEXT
);
