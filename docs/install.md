# Installing Windlass

## Recommended systemd installation

Run on a Linux x86_64 or arm64 server:

```sh
curl -fsSL https://raw.githubusercontent.com/windlass-dev/windlass/main/install/install.sh | sudo sh
```

The installer:

1. verifies Docker Compose v2 and Git, installing Docker when permitted and missing;
2. installs Caddy from its official package repository unless `--no-caddy` is supplied;
3. creates the `windlass` system user and `/var/lib/windlass/{projects,data,backups}`;
4. installs `windlass-docker-proxy.service` on loopback port 2375 and does not grant the
   Windlass user direct socket or `docker`-group access;
5. verifies the release checksum, installs the Windlass binary and hardened systemd unit,
   and starts both services.

Supported flags are `--yes`, `--version vX.Y.Z`, `--no-caddy`, `--no-docker`, and
`--binary PATH` (local/CI installation).

## First run and panel HTTPS

The default service listens on port 8080. Claim it using the one-time token printed in the
journal:

```sh
journalctl -u windlass | grep setup_token
```

Open `http://<server-ip>:8080` temporarily, or use an SSH tunnel when port 8080 is firewalled.
After signing in:

1. create a DNS A/AAAA record such as `windlass.example.com` pointing at the server;
2. open **Settings → Panel domain**;
3. enter the hostname without `https://` and save.

Windlass creates the targeted Caddy object `windlass_panel_route` and adds the hostname to
Caddy certificate automation. It does not modify the Caddyfile or unrelated routes. Removing
the setting deletes only that route. DNS must already resolve to the server and ports 80/443
must reach Caddy.

After the HTTPS hostname works, firewall public port 8080 or add a systemd override setting
`WINDLASS_ADDR=127.0.0.1:8080` and restart Windlass.

## Runtime configuration

| Variable | Default | Purpose |
|---|---|---|
| `WINDLASS_ADDR` | `:8080` | Windlass HTTP listen address |
| `WINDLASS_DATA` | `/var/lib/windlass` | Platform database, keys, plugins, projects, and backups |
| `WINDLASS_PROJECTS` | `$WINDLASS_DATA/projects` | Authoritative Compose project directories |
| `WINDLASS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `WINDLASS_CADDY_ADMIN` | `http://127.0.0.1:2019` | Caddy admin API used for targeted route updates |
| `WINDLASS_PANEL_UPSTREAM` | `127.0.0.1:8080` | Address Caddy dials for the Settings-managed panel route |
| `WINDLASS_NO_SELF_UPDATE` | unset | Disable binary self-update when set |
| `DOCKER_HOST` | Docker client default | Installer sets `tcp://127.0.0.1:2375` in systemd |

The Caddy admin API and Docker proxy must not be exposed publicly.

## Container installation

```sh
curl -fsSLO https://raw.githubusercontent.com/windlass-dev/windlass/main/install/docker-compose.install.yaml
docker compose -f docker-compose.install.yaml up -d
```

This starts Windlass and the restricted socket proxy. It does not bundle Caddy. Application
domains and the panel-domain setting remain unavailable until Windlass can reach an existing
Caddy admin API. Set `WINDLASS_CADDY_ADMIN` and `WINDLASS_PANEL_UPSTREAM` to addresses valid
from the Caddy/Windlass networks.

The example uses a named volume. Replace it with a host bind mount when SSH editing and
filesystem-based recovery are required:

```yaml
volumes:
  - /var/lib/windlass:/var/lib/windlass
```

Container installs set `WINDLASS_NO_SELF_UPDATE`; update them by pulling a new image and
recreating the Windlass container.

## Bare binary

```sh
wget https://github.com/windlass-dev/windlass/releases/latest/download/windlass-linux-amd64
chmod +x windlass-linux-amd64
sudo WINDLASS_DATA=/var/lib/windlass ./windlass-linux-amd64
```

A bare process needs equivalent filesystem permissions, Docker connectivity, Caddy access,
and process supervision. The systemd installer is safer for normal servers.

## Updates and rollback

The Settings update action is supported only by Linux binary installations when self-update
is enabled. It downloads the release binary and `checksums.txt`, verifies SHA-256, keeps
`/usr/local/bin/windlass.previous` when possible, atomically swaps the executable, and exits so
systemd restarts it. Application containers are not restarted.

Manual rollback:

```sh
sudo mv /usr/local/bin/windlass.previous /usr/local/bin/windlass
sudo systemctl restart windlass
```

## Uninstall without removing applications

```sh
sudo systemctl disable --now windlass
sudo systemctl disable --now windlass-docker-proxy
sudo rm /usr/local/bin/windlass
```

Do not delete `/var/lib/windlass/projects` if you want to retain stack files. Existing Docker
containers keep running. Caddy's in-memory routes continue until Caddy reloads/restarts; make
any route needed permanently without Windlass part of the administrator-owned Caddyfile.

See [Life without the panel](life-without-the-panel.md).
