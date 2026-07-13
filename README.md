# Windlass

**A lightweight Docker Compose control plane.** Deploy, route, and manage apps on your own servers — without the weight of a PaaS.

> A windlass is the winch that does a ship's heavy lifting: raising the anchor, hauling the mooring lines. Windlass does the same for your VPS.

## Philosophy

**Docker Compose is the source of truth.** Windlass is only an orchestration layer on top of the tools your server already has: Docker, Compose, Caddy, git, systemd.

- If Windlass is removed, every deployed application keeps running.
- No proprietary deployment formats. Every project is a plain directory with a `compose.yaml` and a `.env` you can edit by hand.
- Caddy owns routing and TLS. Docker owns containers. Git owns source. SQLite stores only platform metadata.
- One static frontend, one Go binary, < 80 MB idle RAM for the whole platform.
- Zero Redis, zero queues, zero Kubernetes, zero Swarm, zero Prometheus.

## Features

- Projects as Compose directories — create, edit, deploy, restart, stop
- Git deployments — GitHub/GitLab webhooks, auto-deploy on push, one-click rollback to previous image digests
- Domains with automatic HTTPS via Caddy + Let's Encrypt
- One-click PostgreSQL, Redis, MySQL, MongoDB
- Encrypted environment variables
- Live deploy logs, container logs, and browser terminal
- Host + container metrics straight from Docker — no metrics stack
- Backups (local + S3-compatible), manual and scheduled
- Idempotent, crash-resumable deployments
- Optional plugins that use zero RAM until enabled

## Install

```sh
curl -fsSL https://get.windlass.sh | sh
```

(Docker Compose and bare-binary installs are also supported — see `docs/install.md`.)

## Development

```sh
make dev      # run backend + Vite dev server
make build    # build the single production binary
make test     # unit tests (no Docker required)
```

Integration tests (real Docker + Caddy) run in CI on Linux: `go test -tags integration ./...`

## License

Apache-2.0
