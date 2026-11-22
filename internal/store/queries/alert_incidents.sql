-- name: ListAlertIncidents :many
SELECT * FROM alert_incidents ORDER BY first_seen_at;

-- name: UpsertAlertIncident :exec
INSERT INTO alert_incidents (key, kind, project, title, message, first_seen_at, seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET title = excluded.title, message = excluded.message,
    seen_at = excluded.seen_at;

-- name: MarkAlertIncidentAlerted :exec
UPDATE alert_incidents SET alerted_at = ? WHERE key = ?;

-- name: DeleteAlertIncident :exec
DELETE FROM alert_incidents WHERE key = ?;
