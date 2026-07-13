# Windlass plugin SDK

A Windlass plugin is an external process — any language, any runtime. When
disabled it is simply not running: zero RAM, zero attack surface.

## Contract

1. Install: a directory under `/var/lib/windlass/plugins/<name>/` containing:
   - `plugin.json` — the manifest
   - the executable named by `command`
2. When enabled, Windlass starts the executable with the environment variable
   `WINDLASS_PLUGIN_ADDR` (e.g. `127.0.0.1:49213`). The plugin must serve
   HTTP on exactly that address.
3. All requests to `/api/v1/plugins/<name>/proxy/*` on the panel are
   forwarded to the plugin with the prefix stripped. Requests are already
   authenticated by the panel (any signed-in user; add your own checks if
   you need role granularity).
4. When disabled or at panel shutdown, the process receives SIGKILL via
   context cancellation — hold no state that can't be lost, or handle
   SIGTERM yourself and persist under your plugin directory.

## Manifest

```json
{
  "name": "hello",             // must equal the directory name
  "version": "0.1.0",
  "description": "What it does",
  "command": "hello",          // executable, relative to the plugin dir
  "ui": true                   // serves a web UI at /
}
```

## Example

`example/` contains a complete Go plugin (~40 lines). Build and install:

```sh
cd example
CGO_ENABLED=0 GOOS=linux go build -o hello .
sudo mkdir -p /var/lib/windlass/plugins/hello
sudo cp hello plugin.json /var/lib/windlass/plugins/hello/
```

Enable it in the panel (Settings → Plugins, admin only), then:

```sh
curl -b <session> http://localhost:8080/api/v1/plugins/hello/proxy/
```

## Principles for plugin authors

- Your plugin must never require the panel to stay working: it can talk to
  Docker or the filesystem itself if the operator grants it, but Windlass
  gives it nothing beyond the HTTP contract above.
- Persist only under your own plugin directory.
- Log to stdout/stderr; the operator sees it in the panel's journal.
