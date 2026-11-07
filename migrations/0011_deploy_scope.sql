-- The deploy scope for API tokens.
--
-- A deploy token reads like a read token and may also deploy, restart, roll
-- back, promote staging and take a backup. It can never write Compose files,
-- env, Git settings or domains. It exists so an agent or CI job that ships
-- code does not also need the power to rewrite a project's .env.
--
-- It is stored as scope 'read' plus this flag, and the API shows it as scope
-- "deploy". Widening the scope CHECK instead would need a table rebuild, and
-- each migration runs in one transaction with foreign keys on, where SQLite
-- ignores PRAGMA foreign_keys: dropping the old api_tokens would fire
-- ON DELETE SET NULL and erase audit_log.token_id. An older binary that does
-- not know the flag treats a deploy token as a read token, which fails safe.

ALTER TABLE api_tokens ADD COLUMN can_deploy INTEGER NOT NULL DEFAULT 0
    CHECK (can_deploy IN (0, 1) AND (can_deploy = 0 OR scope = 'read'));
