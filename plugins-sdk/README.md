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
- Current plugin proxy authorization requires a signed-in user. A plugin needing finer roles
  must implement additional authorization.
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

Windlass does not grant plugins Docker, Caddy, project-filesystem, or secret access. A plugin
has only its process permissions and the HTTP contract above unless the server operator grants
more. Log to stdout/stderr, keep dependencies minimal, validate all plugin input, and never
assume the panel process will stay running.
