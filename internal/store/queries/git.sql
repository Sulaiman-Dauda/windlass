-- name: CreateGitConnection :one
INSERT INTO git_connections (provider, name, token_enc) VALUES (?, ?, ?)
RETURNING *;

-- name: ListGitConnections :many
SELECT * FROM git_connections ORDER BY name;

-- name: GetGitConnection :one
SELECT * FROM git_connections WHERE id = ?;

-- name: DeleteGitConnection :exec
DELETE FROM git_connections WHERE id = ?;

-- name: ConfigureProjectGit :exec
UPDATE projects
SET source = 'git', git_repo = ?, git_branch = ?, auto_deploy = ?,
    git_connection_id = ?, webhook_secret_enc = ?
WHERE id = ?;

-- name: ClearProjectGit :exec
UPDATE projects
SET source = 'manual', git_repo = NULL, git_branch = NULL, auto_deploy = 0,
    git_connection_id = NULL, webhook_secret_enc = NULL
WHERE id = ?;
