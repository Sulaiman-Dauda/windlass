-- name: CreateProject :one
INSERT INTO projects (name, source, git_repo, git_branch, auto_deploy)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetProjectByName :one
SELECT * FROM projects WHERE name = ?;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY name;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ?;

-- name: UpdateProjectGit :exec
UPDATE projects SET git_repo = ?, git_branch = ?, auto_deploy = ? WHERE id = ?;
