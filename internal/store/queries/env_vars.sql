-- name: UpsertEnvVar :exec
INSERT INTO env_vars (project_id, key, value_enc) VALUES (?, ?, ?)
ON CONFLICT(project_id, key) DO UPDATE SET value_enc = excluded.value_enc;

-- name: ListEnvVars :many
SELECT * FROM env_vars WHERE project_id = ? ORDER BY key;

-- name: DeleteEnvVar :exec
DELETE FROM env_vars WHERE project_id = ? AND key = ?;

-- name: DeleteProjectEnvVars :exec
DELETE FROM env_vars WHERE project_id = ?;
