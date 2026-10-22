# Windlass plugin SDK

A plugin is an optional external process installed under
`$WINDLASS_DATA/plugins/<name>`. Disabled plugins do not run.

## Manifest and process contract

`plugin.json` and the executable named by `command` must be in the plugin directory:

```json
{
  "name": "hello",
  "version": "0.1.0",
  "description": "Example plugin",
  "command": "hello",
  "ui": true
}
```

- `name` must equal the directory name.
- `command` must be a relative path inside the plugin directory.
- When enabled, Windlass chooses a loopback address and provides it in
  `WINDLASS_PLUGIN_ADDR`. The process must serve HTTP on exactly that address.
- Requests under `/api/v1/plugins/<name>/proxy/*` are authenticated by Windlass, forwarded to
  the plugin, and have that prefix stripped.
- Any signed-in user or API token can send `GET`, `HEAD` and `OPTIONS` requests through the
  proxy. Every other method needs the `member` role, or a token with Deploy and edit
  access, and so does opening a WebSocket, because once open it carries writes too.
  Anything less gets `403` from Windlass before the plugin sees it. This changed in v0.57:
  before it, a viewer could send any method and open a WebSocket.
- Windlass does not tell the plugin who the caller is. A plugin that needs to tell users
  apart, or to allow some writes to members and not others, has to run its own sign-in.
- Windlass starts enabled plugins when it starts, and starts a plugin when it is enabled.
  It does not restart one that exits; until it is enabled again or Windlass restarts, the
  proxy answers `503 plugin_unavailable`. A plugin that is running but not listening on
  `WINDLASS_PLUGIN_ADDR` gets `502 plugin_unreachable` after two seconds.
- Disabling a plugin or stopping Windlass terminates the process. Persist required state under
  the plugin directory; do not depend on in-memory state surviving.

The `ui` field describes a plugin that serves HTML at its root. Windlass currently exposes the
proxy API but does not automatically add a navigation item or dedicated Settings screen.

## Build and install the example

```sh
cd plugins-sdk/example
CGO_ENABLED=0 GOOS=linux go build -o hello .
sudo mkdir -p /var/lib/windlass/plugins/hello
sudo cp hello plugin.json /var/lib/windlass/plugins/hello/
```

Enable it through the admin API:

```sh
curl -X POST -b <session-cookie-file> \
  https://windlass.example.com/api/v1/plugins/hello/enable

curl -b <session-cookie-file> \
  https://windlass.example.com/api/v1/plugins/hello/proxy/
```

Disable it with `POST /api/v1/plugins/hello/disable`.

## Security and resource guidance

Install only plugins you trust as much as Windlass itself. A plugin runs as the same system
user as the panel and inherits its environment, so it can read what Windlass can read: the
database, the encryption key, project files including `.env`, and the Docker socket proxy.
Windlass gives it no API of its own for any of these, but it does not sandbox it either.

Requests reach the plugin's server without the caller's credentials. The proxy removes the
`Authorization` header and the `windlass_session` cookie, so the plugin process cannot
replay them against the Windlass API. Other cookies pass through. This changed in v0.57:
a plugin that read either of them will no longer see them.

That does not stop a plugin's own pages. They are served from the panel's origin, so a
script on them runs as whoever is viewing it and can call the API with their session.

Log to stdout/stderr, keep dependencies minimal, validate all plugin input, and never
assume the panel process will stay running.
