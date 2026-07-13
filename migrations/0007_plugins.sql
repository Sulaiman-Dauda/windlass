-- Plugins are external processes discovered from the plugins directory.
-- Only the enabled flag is metadata; everything else lives in the plugin's
-- own manifest on disk.

CREATE TABLE plugins (
    name       TEXT PRIMARY KEY,
    enabled    INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
