-- name: InsertAudit :exec
INSERT INTO audit_log (user_id, action, resource_type, resource_id, ip, detail)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAudit :many
SELECT * FROM audit_log ORDER BY id DESC LIMIT ? OFFSET ?;
