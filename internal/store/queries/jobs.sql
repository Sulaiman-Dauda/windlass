-- name: EnqueueJob :one
INSERT INTO jobs (type, payload) VALUES (?, ?) RETURNING *;

-- name: ClaimNextJob :one
UPDATE jobs
SET status = 'running',
    attempts = attempts + 1,
    locked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = (
    SELECT id FROM jobs
    WHERE status = 'queued' AND run_after <= strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    ORDER BY id
    LIMIT 1
)
RETURNING *;

-- name: CheckpointJob :exec
UPDATE jobs
SET step = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: FinishJob :exec
UPDATE jobs
SET status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: ReclaimRunningJobs :execrows
UPDATE jobs
SET status = 'queued', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'running';

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;
