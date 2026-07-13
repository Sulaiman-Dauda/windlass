# Installing Windlass

## One-line install (recommended)

On a fresh Linux server (Ubuntu/Debian/Fedora/Alpine, x86_64 or arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/windlass-dev/windlass/main/install/install.sh | sudo sh
```

The installer:
1. Installs Docker (via get.docker.com) and Caddy (official repos) if missing
2. Creates the `windlass` system user and `/var/lib/windlass/{projects,data,backups}`
3. Downloads the release binary, verifies its sha256, and installs a hardened
   systemd service
4. Prints the URL and how to find the one-time **setup token**

Flags: `--yes` (non-interactive), `--version vX.Y.Z`, `--no-caddy`, `--no-docker`.

First run: open `http://<server>:8080`, paste the setup token from
`journalctl -u windlass | grep setup_token`, create the admin account.

## Docker Compose install

```sh
curl -fsSLO https://raw.githubusercontent.com/windlass-dev/windlass/main/install/docker-compose.install.yaml
docker compose -f docker-compose.install.yaml up -d
```

Self-update is disabled in container mode — pull a newer image instead.

## Bare binary

```sh
wget https://github.com/windlass-dev/windlass/releases/latest/download/windlass-linux-amd64
chmod +x windlass-linux-amd64
sudo WINDLASS_DATA=/var/lib/windlass ./windlass-linux-amd64
```

## Configuration

Everything is environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `WINDLASS_ADDR` | `:8080` | HTTP listen address |
| `WINDLASS_DATA` | `/var/lib/windlass` | State root (db, keys, projects, backups) |
| `WINDLASS_PROJECTS` | `$WINDLASS_DATA/projects` | Compose project directories |
| `WINDLASS_LOG_LEVEL` | `info` | debug/info/warn/error |
| `WINDLASS_NO_SELF_UPDATE` | unset | Set to disable self-update |

## Serving the panel itself over HTTPS

Add a Caddy site for the panel (Windlass never touches config it didn't
create, so this is yours to own), e.g. in `/etc/caddy/Caddyfile`:

```
panel.example.com {
    reverse_proxy localhost:8080
}
```

## Updating

Settings → Updates → **Update now** (binary installs), or:

```sh
curl -fsSL .../install.sh | sudo sh   # reinstall over the top; data is kept
```

Rollback after a bad update: `sudo mv /usr/local/bin/windlass.previous /usr/local/bin/windlass && sudo systemctl restart windlass`.

## Uninstalling — your apps keep running

```sh
sudo systemctl disable --now windlass
sudo rm /usr/local/bin/windlass
```

Every deployed project is a plain directory under
`/var/lib/windlass/projects/` and keeps running under Docker + Caddy.
See [life-without-the-panel.md](life-without-the-panel.md).
