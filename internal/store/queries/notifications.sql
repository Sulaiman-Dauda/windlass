-- name: CreateNotificationChannel :one
INSERT INTO notification_channels (name, kind, config_enc, enabled, events, project)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateNotificationChannel :one
UPDATE notification_channels
   SET name = ?, config_enc = ?, enabled = ?, events = ?, project = ?,
       updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE id = ?
RETURNING *;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = ?;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels ORDER BY name, id;

-- name: ListEnabledNotificationChannels :many
SELECT * FROM notification_channels WHERE enabled = 1 ORDER BY id;

-- name: DeleteNotificationChannel :exec
DELETE FROM notification_channels WHERE id = ?;

-- name: EnqueueNotification :one
INSERT INTO notifications (channel_id, event, project, payload, next_attempt_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: ListDueNotifications :many
-- A turned-off channel's rows wait: turning it back on sends them, unless
-- they are pruned first.
SELECT n.* FROM notifications n
  JOIN notification_channels c ON c.id = n.channel_id AND c.enabled = 1
 WHERE n.status = 'pending' AND n.next_attempt_at <= ?
 ORDER BY n.id
 LIMIT ?;

-- name: MarkNotificationSent :exec
UPDATE notifications
   SET status = 'sent', attempts = attempts + 1, last_error = NULL,
       sent_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE id = ?;

-- name: MarkNotificationRetry :exec
UPDATE notifications
   SET attempts = attempts + 1, last_error = ?, next_attempt_at = ?
 WHERE id = ?;

-- name: MarkNotificationFailed :exec
UPDATE notifications
   SET status = 'failed', attempts = attempts + 1, last_error = ?
 WHERE id = ?;

-- name: ListRecentNotifications :many
SELECT n.id, n.channel_id, c.name AS channel_name, n.event, n.project, n.status,
       n.attempts, n.last_error, n.created_at, n.sent_at
  FROM notifications n JOIN notification_channels c ON c.id = n.channel_id
 ORDER BY n.id DESC
 LIMIT ?;

-- name: PruneNotifications :exec
DELETE FROM notifications WHERE created_at < ?;
