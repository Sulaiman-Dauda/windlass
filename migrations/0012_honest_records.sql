-- What a backup and a failed deploy actually did.
--
-- backups.db_dump says whether the archive holds a database dump:
-- 'included', 'failed' (the project has a database and no dump was taken;
-- db_dump_error says why) or 'none' (no database container was found).
-- It is NULL for backups taken before this migration, which logged a failed
-- dump and recorded nothing, so the panel cannot say either way.
--
-- deployments.failed_step is the step a failed deploy stopped in, because
-- the status column then holds 'failed' and the step is lost. exit_code is
-- the failing command's exit code where there was one, and error_line the
-- first line of its output that reads as an error.
--
-- Plain ADD COLUMN, no CHECK on the new values: a CHECK widened later would
-- need a table rebuild, which migration 0011 explains is unsafe here. The
-- service writes only the values above. An older binary ignores the columns.

ALTER TABLE backups ADD COLUMN db_dump TEXT;
ALTER TABLE backups ADD COLUMN db_dump_error TEXT;

ALTER TABLE deployments ADD COLUMN failed_step TEXT;
ALTER TABLE deployments ADD COLUMN exit_code INTEGER;
ALTER TABLE deployments ADD COLUMN error_line TEXT;
