-- Deployments, their event streams, image artifacts (for rollback), and the
-- persistent job queue that makes deployments resumable (principle 13).

CREATE TABLE deployments (
    id           INTEGER PRIMARY KEY,
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number       INTEGER NOT NULL,     -- per-project sequence
    status       TEXT NOT NULL DEFAULT 'queued' CHECK (status IN
                 ('queued','preparing','syncing','pulling','building',
                  'applying','verifying','succeeded','failed','cancelled')),
    triggered_by TEXT NOT NULL DEFAULT 'manual' CHECK (triggered_by IN
                 ('manual','webhook','rollback','schedule')),
    git_commit   TEXT,
    error        TEXT,
    rollback_of  INTEGER REFERENCES deployments(id),
    started_at   TEXT,
    finished_at  TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, number)
);
CREATE INDEX idx_deployments_project ON deployments(project_id, number DESC);

-- Replayable event log per deployment; SSE clients catch up via seq
-- (Last-Event-ID) and then live-tail.
CREATE TABLE deployment_events (
    id            INTEGER PRIMARY KEY,
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    ts            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    type          TEXT NOT NULL,       -- 'step' | 'log' | 'error' | 'done'
    message       TEXT NOT NULL,
    UNIQUE (deployment_id, seq)
);

-- Image digests per service, recorded after pull/build; enables rollback.
CREATE TABLE deployment_artifacts (
    id            INTEGER PRIMARY KEY,
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    service       TEXT NOT NULL,
    image_ref     TEXT NOT NULL,
    image_digest  TEXT NOT NULL,
    UNIQUE (deployment_id, service)
);

-- Persistent in-process job queue. Steps checkpoint into `step` so a crash
-- resumes the job from the step it was in, not from scratch.
CREATE TABLE jobs (
    id         INTEGER PRIMARY KEY,
    type       TEXT NOT NULL,
    payload    TEXT NOT NULL,          -- JSON
    status     TEXT NOT NULL DEFAULT 'queued' CHECK (status IN
               ('queued','running','done','failed','dead')),
    step       TEXT NOT NULL DEFAULT '',
    attempts   INTEGER NOT NULL DEFAULT 0,
    run_after  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    locked_at  TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_jobs_status ON jobs(status, run_after);
