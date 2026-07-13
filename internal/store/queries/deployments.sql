-- name: CreateDeployment :one
INSERT INTO deployments (project_id, number, triggered_by, rollback_of)
VALUES (
    ?,
    (SELECT COALESCE(MAX(d2.number), 0) + 1 FROM deployments d2 WHERE d2.project_id = ?),
    ?,
    ?
)
RETURNING *;

-- name: GetDeploymentByID :one
SELECT * FROM deployments WHERE id = ?;

-- name: GetDeployment :one
SELECT d.* FROM deployments d
JOIN projects p ON p.id = d.project_id
WHERE p.name = ? AND d.number = ?;

-- name: ListDeployments :many
SELECT d.* FROM deployments d
JOIN projects p ON p.id = d.project_id
WHERE p.name = ?
ORDER BY d.number DESC
LIMIT ?;

-- name: LatestDeploymentID :one
SELECT id FROM deployments WHERE project_id = ? ORDER BY number DESC LIMIT 1;

-- name: CountActiveDeployments :one
SELECT COUNT(*) FROM deployments
WHERE project_id = ?
  AND status NOT IN ('succeeded', 'failed', 'cancelled');

-- name: SetDeploymentStatus :exec
UPDATE deployments SET status = ? WHERE id = ?;

-- name: MarkDeploymentStarted :exec
UPDATE deployments
SET status = ?, started_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: FinishDeployment :exec
UPDATE deployments
SET status = ?, error = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: SetDeploymentCommit :exec
UPDATE deployments SET git_commit = ? WHERE id = ?;

-- name: InsertDeploymentEvent :one
INSERT INTO deployment_events (deployment_id, seq, type, message)
VALUES (
    ?,
    (SELECT COALESCE(MAX(e2.seq), 0) + 1 FROM deployment_events e2 WHERE e2.deployment_id = ?),
    ?,
    ?
)
RETURNING seq;

-- name: ListDeploymentEvents :many
SELECT * FROM deployment_events
WHERE deployment_id = ? AND seq > ?
ORDER BY seq
LIMIT ?;

-- name: InsertDeploymentArtifact :exec
INSERT INTO deployment_artifacts (deployment_id, service, image_ref, image_digest)
VALUES (?, ?, ?, ?)
ON CONFLICT(deployment_id, service) DO UPDATE
SET image_ref = excluded.image_ref, image_digest = excluded.image_digest;

-- name: ListDeploymentArtifacts :many
SELECT * FROM deployment_artifacts WHERE deployment_id = ? ORDER BY service;
