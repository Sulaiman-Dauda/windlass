-- name: CreateAPIToken :one
INSERT INTO api_tokens (public_id, user_id, name, secret_hash, scope, can_read_secrets, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAPITokenByPublicID :one
SELECT * FROM api_tokens WHERE public_id = ?;

-- name: ListAPITokensForUser :many
SELECT * FROM api_tokens WHERE user_id = ? ORDER BY id DESC;

-- name: ListAPITokens :many
SELECT t.*, u.email AS user_email
FROM api_tokens t JOIN users u ON u.id = t.user_id
ORDER BY t.id DESC;

-- name: GetAPIToken :one
SELECT * FROM api_tokens WHERE id = ?;

-- name: RevokeAPIToken :exec
UPDATE api_tokens SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?;
