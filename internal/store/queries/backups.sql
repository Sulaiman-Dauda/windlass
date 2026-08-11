-- name: CreateBackup :one
INSERT INTO backups (project_id, kind, destination) VALUES (?, ?, ?)
RETURNING *;

-- name: FinishBackup :exec
UPDATE backups
SET status = ?, path = ?, size = ?, error = ?,
    finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: GetBackup :one
SELECT b.* FROM backups b
JOIN projects p ON p.id = b.project_id
WHERE p.name = ? AND b.id = ?;

-- name: ListProjectBackups :many
SELECT b.* FROM backups b
JOIN projects p ON p.id = b.project_id
WHERE p.name = ?
ORDER BY b.id DESC
LIMIT 50;

-- name: ListBackupsForPrune :many
SELECT * FROM backups
WHERE project_id = ? AND status = 'done' AND destination = ?
ORDER BY id DESC;

-- name: DeleteBackup :exec
DELETE FROM backups WHERE id = ?;

-- name: UpsertBackupSchedule :exec
INSERT INTO backup_schedules (project_id, interval, destination, retention_count, enabled)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(project_id) DO UPDATE SET
    interval = excluded.interval,
    destination = excluded.destination,
    retention_count = excluded.retention_count,
    enabled = excluded.enabled;

-- name: GetBackupSchedule :one
SELECT * FROM backup_schedules WHERE project_id = ?;

-- name: ListDueSchedules :many
SELECT s.*, p.name AS project_name
FROM backup_schedules s
JOIN projects p ON p.id = s.project_id
WHERE s.enabled = 1;

-- name: TouchScheduleRun :exec
UPDATE backup_schedules
SET last_run_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;
