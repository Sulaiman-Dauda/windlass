# 11 First-run checks

Status: built 13/11/2026 on `fix/pr8-logs-drift-preflight` (v0.62 PR 8, F10, #116, #117).
Not part of the MCP scaffold. Ships in v0.62.

## Goal

A fresh server says what it is missing before anyone deploys to it. The installer names a
firewall that blocks the panel or HTTPS, the setup page can check the server with the setup
token, and the logs pane holds a reader's place to the pixel past its cap.

## Fills

- `internal/auth`: `Service.SetupTokenValid`, constant-time, false once any user exists.
- `internal/api/preflight.go`: `GET /system/preflight` with its own 20 a minute per-IP
  limit. Uses `Ping`, `Host().Metrics` and `Host().HTTPCheck`; no new agent method.
- `install/install.sh`: read-only `ufw` or `firewalld` check after the service starts.
- `web/src/components/TermPane.tsx`: the scroll handler keeps the anchor on its own move.
- Web: `usePreflight` and the Setup page button. OpenAPI follows.

## Touches

- `internal/auth/service.go`, `internal/api/api.go`, `internal/api/preflight.go`
- `internal/server/preflight_api_test.go`
- `api/openapi.yaml`, `docs/install.md`, `install/install.sh`
- `web/src/api/auth.ts`, `web/src/pages/Setup.tsx`, `web/src/components/TermPane.tsx`
- `web/e2e/logs-pane.spec.ts`

## Decisions

- Before the claim only the setup token opens the checks; after it only an admin session
  or an admin-scope token. Viewers, members and narrower tokens get 403.
- The checks change nothing. A port counts as listening unless the dial is refused, so a
  TLS error on a bare IP is still Caddy. They cannot see a firewall in front of the server,
  and the detail says so.
- Free disk warns under 5 GiB, a constant for now.
- The installer never changes a firewall. ufw app-profile rules and port ranges read as
  closed, and firewalld is checked in its default zone only; the message says "looks closed".

## Done means

`make test`, `make lint`, `scaffold-check`, vitest and the web build pass. Server tests for
token, wrong token, token after the claim, admin, viewer, missing pieces and a refused port.
The logs-pane spec fails before the fix (56 px) and passes after. The ufw matcher run under
dash, shellcheck clean. Setup page screenshots in light and dark. A fresh-context review.
