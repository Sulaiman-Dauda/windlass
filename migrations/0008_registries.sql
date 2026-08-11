-- Container registry credentials.
--
-- Windlass pulls private images with `docker compose pull`, which reads the
-- host's Docker config. Nothing ever wrote one, so a private image failed with
-- "unauthorized" and the deployment died at the pulling step. Four of five
-- projects on the first real installation were in that state.
--
-- The credential is applied with a real `docker login`, so it lands in the
-- host's Docker config and a plain `docker compose pull` works whether or not
-- Windlass is running. That is deliberate: the panel is removable from the
-- application runtime path, and holding the credential only inside Windlass
-- would have quietly made it mandatory.
--
-- One row per registry host. Per-project credentials can be added later by
-- giving this table a nullable project_id without moving anything.

CREATE TABLE registry_credentials (
    id         INTEGER PRIMARY KEY,
    -- 'ghcr.io', 'docker.io', 'registry.gitlab.com'.
    host       TEXT NOT NULL UNIQUE,
    username   TEXT NOT NULL,
    secret_enc BLOB NOT NULL,          -- AES-256-GCM, same box as git tokens
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    -- When the host was last logged in successfully, so the panel can say
    -- whether the credential has ever actually worked rather than only that
    -- somebody typed one in.
    verified_at TEXT
);
