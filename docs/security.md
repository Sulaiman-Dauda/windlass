# Security

Windlass deploys containers, writes Caddy routes and stores credentials, so it is as
powerful as root on the server it runs on. This page sets out what runs with which
privileges, what each part can reach, and what the installer downloads, so you can decide
where to run it and who to let in.

To report a vulnerability, see [Reporting a vulnerability](#reporting-a-vulnerability). Never
open a public issue for one.

## The short version

- Anyone who can edit and deploy a project can become root on the host. A Compose file can
  ask for a privileged container or mount `/`, and Windlass deploys what the file says.
- Treat the server as single-tenant. The Docker socket proxy and the Caddy admin API listen
  on loopback without authentication, so any process on the host can use them.
- Windlass has had no outside security review. It is in production use, on servers whose
  owners can rebuild them.

## What runs as root

| Part | Runs as | What it does with that |
| --- | --- | --- |
| `install.sh` | root | Installs Docker, Git and Caddy if missing, the binary and two systemd units, and creates the `windlass` user |
| Docker | root | Docker's own daemon, unchanged |
| `windlass-docker-proxy.service` | root, to run `docker run` | Starts the socket proxy container with every capability dropped and `no-new-privileges` |
| The update hook in `windlass.service` | root | Before each start, moves a binary the panel staged in `/var/lib/windlass/data/update/` into `/usr/local/bin`, keeping the old one as `windlass.previous`. Nothing else |
| `windlass` | the `windlass` system user | The panel. No login shell, no capabilities, and a systemd sandbox that lets it write only `/var/lib/windlass` |
| Caddy | its package's own user, normally `caddy` | Serves your domains and the panel's |
| Your containers | whatever their image and Compose file say | Your applications |

Because the panel stages its own updates, anything running as `windlass` can choose the next
binary. That binary still runs as `windlass`, not root.

## Docker access

The `windlass` user is not in the `docker` group, and the installer removes it if it finds it
there. The panel reaches Docker through
[docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy) on
`127.0.0.1:2375`, which allows the API families Compose needs (containers, images, volumes,
networks, builds, exec, events, system and version) and refuses the rest, such as Swarm,
services, secrets and plugins.

That narrows what the panel can ask Docker for. It does not make Docker access safe: creating
a container is allowed, and a privileged container with the host's filesystem mounted is root
on the host. So these can all become root:

- an admin or a `member`, who can edit and deploy projects
- an API token with **Deploy and edit** or **Admin** access
- a **Deploy only** token on a Git project, if its holder can push to the repository
- any process or account on the host that can open a loopback connection, including a
  container with `network_mode: host`

Give `member` only to people you would trust with root, and keep those tokens as carefully as
an SSH key. Do not give other people shell accounts on a Windlass server.

The [container install](./install.md) puts the proxy on an internal Docker network that only
the Windlass container joins, rather than on host loopback. Self-update is off in that mode.

## Caddy

Windlass talks to Caddy's admin API on `127.0.0.1:2019`. On a server without Caddy, the
installer replaces the package's welcome site with a Caddyfile that only sets that admin
address, keeping the original as `/etc/caddy/Caddyfile.default.bak`. A Caddy that was already
installed is left as it was.

Windlass changes only the objects it owns, by `@id`: `windlass_routes`, `windlass_panel_route`
and their `windlass_route_*` children. It never loads or replaces the whole configuration, so
sites and routes you add to Caddy yourself are not touched. See
[Domains and HTTPS](./domains-and-https.md).

## The panel

- It listens on port 8080 on every interface, over plain HTTP, until you give it a domain.
  Firewall that port once the domain works, or set `WINDLASS_ADDR=127.0.0.1:8080`. See
  [Install](./install.md).
- A new instance is claimed with a one-time token printed to the server log, so whoever
  reaches the page first cannot claim it.
- Sessions last seven days and are rows on the server, so signing out or deleting a user ends
  them at once.
- Requests that change state are refused when a browser marks them as cross-origin.
- Stored credentials (S3 keys, Git, GitHub App and registry credentials, OAuth secrets,
  TOTP secrets and the copy of each project's env values in SQLite) are encrypted with
  `data/secret.key`. The key sits beside the database, so a copy of the data directory
  is a copy of every one of them. Registry credentials are also stored by `docker login`
  itself, unencrypted as Docker always keeps them, in
  `/var/lib/windlass/.docker/config.json`.

Roles and what each can do are in [Users and access](./users-and-access.md).

## API tokens

Only admins create tokens, for their own account, under **Settings, API tokens**.

| Access | Acts as at most |
| --- | --- |
| **Read only** | `viewer` |
| **Deploy only** | a `viewer` that can also deploy, restart, roll back, promote and take a backup |
| **Deploy and edit** | `member` |
| **Admin** | `admin` |

A token never has more access than its owner's current role, so demoting a user demotes their
tokens. Windlass stores only a SHA-256 of the secret and shows the token once. Every token
expires, after 90 days unless you choose between 1 and 365. Tokens start `wl_pat_`, so
GitHub push protection and similar scanners can spot one committed by mistake. See
[API](./api.md#api-tokens).

## AI agents

The [MCP server](./mcp.md) takes the same tokens with the same limits, plus some of its own:
it accepts a bearer token only and refuses any request carrying an `Origin` header, so a web
page cannot reach it; it never returns env values and scrubs them from logs, with gaps listed
on that page; it has five writes, each needing `confirm: true`; and it allows 120 calls a
minute per token.

## Plugins

A plugin runs as the `windlass` user with the panel's environment, so it can read what the
panel can read, the database and encryption key included, and use the Docker socket proxy.
It is not sandboxed. Install only plugins you trust as much as Windlass itself. See the
[plugin SDK](https://github.com/Sulaiman-Dauda/windlass/tree/main/plugins-sdk).

## Backups

A project backup is a compressed archive of the project directory, `.env` included, plus a
database dump where there is one. Windlass does not encrypt it. Anyone who can read the
backups directory or the S3 bucket can read every secret in those projects, so restrict the
bucket to the one key Windlass uses. See [Backups and restore](./backups-and-restore.md).

## What the installer fetches

`curl -fsSL https://get.windlass.run | sudo sh` downloads:

1. **get.windlass.run**, a Cloudflare Worker that only redirects to `install.sh` attached to
   the latest GitHub release (or to the release named in the path, such as
   `get.windlass.run/v0.60`). The script you run is the one that shipped with that release's
   binaries, not whatever is on `main`.
2. **get.docker.com**, Docker's own install script, only when Docker is missing and you say yes.
3. **Caddy's package repository**, only when Caddy is missing: Cloudsmith for apt, the Caddy
   COPR for dnf, or Alpine's own repository for apk.
4. Git from your distribution's packages, when it is missing.
5. From **api.github.com**, the latest release tag, unless you pass `--version`.
6. From the **GitHub release**, the binary for your architecture and `checksums.txt`. The
   script stops if the checksum does not match.

The checksum catches a broken download. It does not prove the release is genuine, because
`checksums.txt` is in the same release as the binary and whoever could replace one could
replace the other. `install.sh` itself has no checksum or attestation: read it before you
run it.

### Verify first, then install

Every release binary and container image carries GitHub build provenance, stored outside the
release, which ties the exact bytes to the workflow run and commit that built them. To check
before anything runs as root:

```sh
gh release download v0.60 --repo Sulaiman-Dauda/windlass \
  --pattern 'windlass-linux-amd64' --pattern install.sh
gh attestation verify windlass-linux-amd64 --repo Sulaiman-Dauda/windlass
less install.sh
sudo sh install.sh --binary ./windlass-linux-amd64
```

`--binary` installs the file you verified instead of downloading another. Use `arm64` in
place of `amd64` on ARM servers.

### Updates from the panel

**Software updates** under **Settings, System** downloads the new binary and checks it
against the release's `checksums.txt`, and refuses a release that has none. It does not check the attestation, so
it is protected against a corrupted download, not a rewritten release. Where that matters,
verify the new version by hand first, as above.

## Reporting a vulnerability

Use [Report a vulnerability](https://github.com/Sulaiman-Dauda/windlass/security/advisories/new)
on GitHub, which only the maintainer can see. Say what an attacker can do, how to reproduce it
on a throwaway server, and the version you tested.
[SECURITY.md](https://github.com/Sulaiman-Dauda/windlass/blob/main/SECURITY.md) has what is in
scope and what to expect.
