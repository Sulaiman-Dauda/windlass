-- name: InsertAudit :exec
INSERT INTO audit_log (user_id, token_id, action, resource_type, resource_id, ip, detail)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListAudit :many
SELECT * FROM audit_log ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: SearchAudit :many
-- Newest first. Every filter is optional; before_id pages by keyset.
-- action_prefix matches a family such as "project.".
SELECT a.id, a.ts, a.user_id, a.token_id, a.action, a.resource_type, a.resource_id, a.ip, a.detail,
       COALESCE(u.email, '') AS user_email, COALESCE(t.name, '') AS token_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN api_tokens t ON t.id = a.token_id
WHERE (sqlc.narg(action) IS NULL OR a.action = sqlc.narg(action))
  AND (sqlc.narg(action_prefix) IS NULL OR substr(a.action, 1, length(sqlc.narg(action_prefix))) = sqlc.narg(action_prefix))
  AND (sqlc.narg(user_id) IS NULL OR a.user_id = sqlc.narg(user_id))
  AND (sqlc.narg(resource_type) IS NULL OR a.resource_type = sqlc.narg(resource_type))
  AND (sqlc.narg(resource_id) IS NULL OR a.resource_id = sqlc.narg(resource_id))
  AND (sqlc.narg(since) IS NULL OR a.ts >= sqlc.narg(since))
  AND (sqlc.narg(until) IS NULL OR a.ts < sqlc.narg(until))
  AND (sqlc.narg(before_id) IS NULL OR a.id < sqlc.narg(before_id))
ORDER BY a.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListAuditActions :many
SELECT DISTINCT action FROM audit_log ORDER BY action;
