# 03 MCP write tools and the deploy scope

Status: built 07/11/2026, committed on `feat/mcp`. The integration suite is not fully green
on this laptop (below).
Phase 3. Depends on 01.

## Goal

An agent holding a `deploy` token can deploy, restart, roll back, promote staging and take a
backup through MCP, and nothing else that writes. Every destructive tool says so in its
annotations and refuses to run without `confirm: true`. Every write lands in the audit log
twice: once from the REST route it called (with the token named) and once as `mcp.call`.

## Fills

- `internal/mcp`: five tools, `deploy`, `restart`, `rollback`, `promote`, `create_backup`;
  `tools/list` filtered by what the token can do; `confirm` handling; the `mcp.call` audit
  hook on success.
- The `deploy` scope in `internal/auth`, the token routes in `internal/api`, the panel's token
  form and the API docs.

## Decisions

Settled by Pablo 30/10/2026: a `deploy` scope lands in this unit. Settled during the build 07/11/2026,
flagged for Pablo:

- **Stored as a flag, not a fourth scope value.** Migration 0011 adds
  `api_tokens.can_deploy`; a deploy token is stored as scope `read` plus the flag, and the API
  shows it as scope `deploy`. The spec said migration 0010 with a table rebuild to widen the
  scope `CHECK`. 0010 is taken (staging), and the rebuild is unsafe here: each migration runs in
  one transaction with `foreign_keys(ON)`, SQLite ignores `PRAGMA foreign_keys` inside a
  transaction, so dropping the old `api_tokens` would fire `ON DELETE SET NULL` and erase
  `audit_log.token_id` on every install. With the flag, an older binary reads a deploy token as
  a read token.
- **Allowlist, not denylist.** A deploy token acts as a `viewer` everywhere, plus five routes
  behind `auth.RequireDeployer`: create deployment, rollback, promote, create backup, restart.
  A member route added later stays closed to deploy tokens unless someone opts it in.
- **Restart only.** A deploy token may restart a project but not start or stop it
  (`POST /projects/{name}/actions/restart` is its own route). The spec lists restart only.
- **Destructive:** `deploy`, `restart`, `rollback`, `promote` carry `destructiveHint: true` and
  a required `confirm` that must be `true`. `create_backup` is additive (a manual backup never
  prunes), so it has neither.
- **Writes outlive the client.** A write tool's in-process call is not cancelled when the MCP
  client hangs up, so a restart or backup is never cut off halfway. It waits a bounded time;
  past that, the tool says the work is still running and how to check it.
- **No compose or env writes, no new routes for agents.** The tools call the same routes as
  the panel.

## Touches

`internal/mcp/`
`internal/auth/apitoken.go`, `internal/auth/middleware.go`, `internal/auth/*_test.go`
`internal/api/tokens.go`, `internal/api/projects.go`, `internal/api/deployments.go`,
`internal/api/backups.go`, `internal/api/*_test.go`
`internal/store/queries/api_tokens.sql`, `internal/store/db/` (sqlc generate)
`migrations/0011_deploy_scope.sql`, `internal/store/migrate_test.go`
`internal/server/mcp_api_test.go`, `internal/server/tokens_api_test.go`
`internal/api/api.go` (`MCPAudit`), `cmd/windlass/main.go` (wires it)
`api/openapi.yaml`, `docs/api.md`, `web/src/pages/Settings.tsx`
`docs/build/03-mcp-write-tools.md`

## Found while building

- A tool call with no body passed a nil `Body` to the route; the backup handler decodes
  `r.Body` and panicked. `newAPIRequest` now sends `http.NoBody`. The router test fails
  without it.
- A missing `confirm` said only "confirm is required", so a model could just add it. Missing
  and false now both say to ask the user first.

## Not in this unit

- Start, stop, domains, env, files, Git settings, staging create or recreate, restore.
- `docs/mcp.md`, client snippets: unit 4. OAuth: unit 5. The live host.

## Done when

- [x] `go build ./... && go vet ./... && scaffold-check` pass
- [x] Tests: a deploy token can call the five routes and is refused (403) on env, files, Git
      settings, domains, staging settings, start and stop; read tokens see only read tools
      and get a clear error from a write tool; `confirm` missing or false never reaches the
      API; `mcp.call` is written once per successful write tool, with the token id
- [x] Migration test: 0011 on a database holding tokens and audit rows keeps every
      `audit_log.token_id`
- [x] `make test` passes; `cd web && npm run build` passes; `make lint` 0 issues
- [ ] `make test-integration` passes (Docker required). On this laptop (rootless Podman, no
      Caddy) everything passes except `TestDumpDatabaseRealContainers`, whose database
      containers exit 1 here, and it fails the same way on a clean `4fb1513` checkout; the
      Caddy test skips. Needs a run on CI or a Docker host.
- [x] Scratch instance: a deploy token deploys, restarts and backs up `mcp-scratch` through
      MCP; the audit log shows both lines for each. 07/11/2026: deploy #2 and rollback to #1
      (#3) succeeded, restart and a local backup answered, promote said there is no staging,
      a REST stop with the token got 403; audit rows 8 to 15 are four pairs, all token 2.
      The panel's token form shows "Deploy only" and turns the env switch off for it.
