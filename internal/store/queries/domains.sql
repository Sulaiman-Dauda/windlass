-- name: CreateDomain :one
INSERT INTO domains (project_id, hostname, service, container_port)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: ListProjectDomains :many
SELECT * FROM domains WHERE project_id = ? ORDER BY hostname;

-- name: ListAllDomains :many
SELECT d.*, p.name AS project_name
FROM domains d
JOIN projects p ON p.id = d.project_id
ORDER BY d.hostname;

-- name: GetDomain :one
SELECT d.* FROM domains d
JOIN projects p ON p.id = d.project_id
WHERE p.name = ? AND d.hostname = ?;

-- name: DeleteDomain :exec
DELETE FROM domains WHERE id = ?;
