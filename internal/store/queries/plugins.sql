-- name: SetPluginEnabled :exec
INSERT INTO plugins (name, enabled) VALUES (?, ?)
ON CONFLICT(name) DO UPDATE SET
    enabled = excluded.enabled,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: ListEnabledPlugins :many
SELECT name FROM plugins WHERE enabled = 1;
