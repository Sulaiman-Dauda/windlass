# 16 Alert channels and outbox

Status: built 22/11/2026 on `feat/v063-u13-a1-outbox` (v0.63 U13 A1). No MCP surface
(ARCHITECTURE.md). Ships in v0.63; A2 adds the events.

## Goal

Admins add alert channels (webhook signed with HMAC-SHA256, ntfy, Discord, Slack, SMTP
email) with Send test. Alerts are rows in an SQLite outbox, sent by one goroutine with
retries (1m, 5m, 15m, 1h, 3h), so a receiver that is down loses nothing.

## Fills

- `migrations/0013_notifications.sql`: `notification_channels`, `notifications`.
- `internal/store/queries/notifications.sql`: channel CRUD, enqueue, due rows of enabled
  channels, mark sent, retry or failed, recent, prune after 30 days.
- `internal/notify`: `Service` (channels, `Enqueue`, `Run`, `Drain`), the five senders, the
  dial check that refuses private, loopback, link-local and shared addresses after DNS
  (SMTP may use loopback).
- `internal/api/notifications.go`: `/api/v1/notifications`, admin only; writes and tests
  need a browser session and are audited.
- Panel: Settings, Alerts. Docs: `docs/alerts.md`.

## Touches

- `internal/notify/`, `internal/store/queries/notifications.sql`, `internal/store/db/`,
  `migrations/0013_notifications.sql`, `internal/api/`, `internal/server/` (tests),
  `cmd/windlass/main.go`, `api/openapi.yaml`, `scaffold.json`, `docs/`, `web/src/`

## Leaves alone

`internal/mcp`, the deploy pipeline, Caddy, attention kinds (A2 wires them).

## Rules

- Secrets sealed with `secrets.Box`, never returned; a changed destination drops them.
- A failing channel is skipped for the rest of a pass; a failed outbox write ends it.
- An empty event filter means every problem; deploy succeeded only when picked.

## Done means

`go test ./internal/notify ./internal/server`, `make lint`, web checks, scaffold-check.
