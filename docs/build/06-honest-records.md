# 06 Honest records

Status: built 12/11/2026 on `feat/honest-records` (v0.62 PR 2, B0 and F1). Not part of the
MCP scaffold. Ships in v0.62.

## Goal

A backup row says whether its archive holds a database dump, and a failed deploy says where
and why it stopped, so the panel, API and MCP never report a backup or deploy as better than
it was.

## Fills

- `migrations/0012_honest_records.sql`: `backups.db_dump`, `backups.db_dump_error`,
  `deployments.failed_step`, `deployments.exit_code`, `deployments.error_line`. Added
  columns only, no CHECK constraints, so no table rebuild (the 0011 hazard).
- `internal/store/queries`: `FinishBackup` sets the dump fields; `RecordDeploymentFailure`.
- `internal/agent`: `SpoolFile`, and `ArchiveProject` takes extras as spooled file paths.
  Internal only; the plugin SDK is unchanged.
- `internal/backups`: the dump is spooled to disk and recorded as included, failed with a
  reason, or none. A database compose service with no container is failed, not none.
- `internal/deploy/failure.go`: step, exit code and the last error-looking log line.
- API DTOs, OpenAPI schemas, MCP scrubbing of `error_line` and `db_dump_error`, panel.

## Decisions

- Rows from before 0012 have no `db_dump`; the panel shows "Database dump not recorded".
- The archive walk skips `.windlass/backup`, where a restore leaves its dump, so an old dump
  is never archived as new.
- Leftover temp files in the backups dir are removed only when over 6 hours old.

## Done means

`make test`, `make lint`, `make test-integration` (dump tests need `,z` under SELinux
locally), a scratch dump of over 2 GB with idle RSS under budget, a failed pull recorded with
step, exit code and line, and a v0.61 database upgraded through 0012 on the release check.
