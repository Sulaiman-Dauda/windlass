-- Staging environments.
--
-- A staging project names its live project in its own .windlass.json; this
-- column is the rebuildable index of that link, refreshed by reconcile.
-- promoted_from marks a live deployment that re-applies the commit and image
-- digests a staging deployment verified. Both are nullable and ignored by
-- older releases, so reverting the binary needs no down migration.

ALTER TABLE projects ADD COLUMN staging_of TEXT;
ALTER TABLE deployments ADD COLUMN promoted_from INTEGER REFERENCES deployments(id) ON DELETE SET NULL;
