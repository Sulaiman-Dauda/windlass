# 15 Staging password

Status: built 21/11/2026 on `feat/u12-staging-password` (v0.63 U12, S1). Fills the
`get_staging` `password_protected` field agreed in ARCHITECTURE.md "Additions in v0.63".
Ships in v0.63.

## Goal

Staging's domains ask for a password, because a staging can hold a copy of live's data.
Choice 5 (a): on by default for new stagings. User `staging`; a generated 20-character
password or a chosen one of 12 to 72 bytes, shown once. Only the bcrypt hash (cost 12) is
kept, in staging's `.windlass.json` under `staging.protect`, so Caddy keeps the route if
Windlass stops.

## Fills

- `internal/projects/staging.go`: `StagingProtect`, `NewStagingProtect`,
  `GenerateStagingPassword`, `SetStagingProtect`, `StagingProtection` (any `protect` counts,
  even on a name mismatch).
- `internal/agent/local/proxy.go`: Caddy `authentication` handler with `http_basic` and
  `hash_cache`, first in the route; read back in `CurrentRoutes`.
- `internal/proxy/service.go`: staging routes get `BasicAuth`; an unreadable or invalid hash
  skips that route only.
- `internal/deploy/pipeline.go`, `internal/agent/local/host.go`: checks on a protected
  staging dial the container; if it cannot be reached, the URL, and the password prompt
  counts as the route being up (logged).
- `internal/api/staging.go`: `PUT /projects/{name}/staging/password`, session only, audited
  without the password. `internal/api/projects.go`: hash masked in file reads; token writes
  that change `protect` refused.
- Panel: create dialog switch and field, created dialog, card row with New password and
  Turn off.

## Touches

- `internal/projects/`, `internal/staging/`, `internal/proxy/`, `internal/deploy/`,
  `internal/agent/`, `internal/api/`, `internal/server/` (tests), `internal/mcp/tools.go`
  (description only),
  `api/openapi.yaml`, `docs/`, `web/src/`

## Leaves alone

Live routes, migrations, locked MCP signatures, the routes object outside staging routes.

## Done means

`internal/server/staging_password_test.go`, `internal/deploy/staging_password_test.go`,
`internal/agent/local/httpcheck_dial_test.go` and `TestBuildRoutesObjectBasicAuthFirst`
pass; the proxy integration test gets 401 without the password, 200 with it, and live
unaffected on real Caddy; the card and dialogs render in light and dark.
