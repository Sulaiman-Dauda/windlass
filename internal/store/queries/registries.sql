-- name: UpsertRegistryCredential :one
INSERT INTO registry_credentials (host, username, secret_enc, updated_at)
VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
ON CONFLICT(host) DO UPDATE SET
    username   = excluded.username,
    secret_enc = excluded.secret_enc,
    updated_at = excluded.updated_at
RETURNING *;

-- name: ListRegistryCredentials :many
SELECT * FROM registry_credentials ORDER BY host;

-- name: GetRegistryCredential :one
SELECT * FROM registry_credentials WHERE host = ?;

-- name: DeleteRegistryCredential :exec
DELETE FROM registry_credentials WHERE id = ?;

-- name: MarkRegistryVerified :exec
UPDATE registry_credentials
   SET verified_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE host = ?;
