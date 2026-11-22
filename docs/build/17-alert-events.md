# 17 Alert events

Status: built 22/11/2026 on `feat/v063-u13-a2-events` (v0.63 U13 A2). No MCP surface
(ARCHITECTURE.md). Ships in v0.63 with A1 (16).

## Goal

Fill the A1 outbox: one alert per incident, one recovery, a re-alert at most every 24 h,
for failed deploys, the Needs you kinds, crashed containers, restart loops, unhealthy
containers and certificates under 14 days.

## Fills

- `migrations/0014_alert_incidents.sql`: open incidents, so a restart neither repeats nor
  loses one.
- `internal/alerts`: a one-minute sweep over the Needs you list, the container list and the
  daily certificate dial; Docker die events for crashes and restart loops; deployment.done
  for deploy succeeded and an immediate sweep. Thresholds in the settings table.
- `internal/api/notifications.go`: `GET` and `PUT /api/v1/notifications/settings`.
- `internal/agent`: Compose project, service and exit code on Docker events (additive);
  the fake can emit events.
- Panel: Settings, Alerts, When to alert. Docs: `docs/alerts.md`.

## Touches

- `internal/alerts/`, `migrations/0014_alert_incidents.sql`,
  `internal/store/queries/alert_incidents.sql`, `internal/store/db/`, `internal/agent/`
  (event fields, fake emitter), `internal/api/notifications.go`, `cmd/windlass/main.go`,
  `api/openapi.yaml`, `scaffold.json`, `docs/alerts.md`, `web/src/components/AlertChannels.tsx`

## Leaves alone

`internal/mcp`, the deploy pipeline, Caddy, the notify senders.

## Rules

- Restart loops count die events, because the container list has no restart count and an
  inspect per container per minute is too dear.
- A kind is only cleared by a sweep that read its source: a failed list or Docker call
  clears nothing.
- A die within a minute of kill or stop is a stop, not a crash.

## Done means

`go test ./internal/alerts`, `go test -tags integration ./internal/alerts` (a real crash
loop gives one alert and one recovery), `make lint`, web checks, scaffold-check.
