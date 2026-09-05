# Windlass

Windlass is a lightweight, self-hosted Docker Compose control plane. It provides projects,
Git deployments, domains with HTTPS, logs, terminals, metrics, backups, templates, users,
and updates without replacing Docker Compose with a proprietary runtime.

## Source-of-truth model

Application configuration lives under the projects directory:

```text
<projects>/<name>/
├── compose.yaml       # services, images, ports, volumes, networks, env references, limits
├── .env               # optional standard dotenv file; mode 0600
└── .windlass.json     # source repository, branch, auto-deploy, and domains
```

Windlass executes the real `docker compose` CLI in that directory. Hand-editing the files
and running `docker compose up -d` is supported. The Files and Environment screens read the
same files, and **Scan stacks directory** rebuilds the SQLite project/domain index.

SQLite is not application configuration. It contains platform state such as users, sessions,
audit entries, deployment history, jobs, backup records, settings, encrypted credentials,
and rebuildable project/domain/environment indexes. Removing SQLite does not stop running
containers; filesystem scanning restores the application index, but platform history and
accounts require a platform backup.

## Runtime model

- One Go process serves the API and embedded React frontend.
- Docker Compose performs deployments.
- Caddy owns routing and certificates. Windlass changes only its tagged route objects.
- A loopback-only Docker socket proxy exposes the API families Compose needs. The `windlass`
  system user is not a member of the `docker` group.
- No Redis, Kubernetes, Swarm, Prometheus, or separate job service is required.
- CI enforces an idle RSS budget below 40 MiB for the Go process.

Stopping or removing Windlass does not stop deployed applications. See
[Life without the panel](docs/life-without-the-panel.md).

## Main features

- Manual, Git, webhook, template, and rollback deployments
- Crash-resumable deployment jobs and replayable deployment events
- Caddy application domains and a Settings-managed HTTPS domain for the panel itself
- Raw file editing plus individual or bulk-paste environment variable editing
- Compose-native CPU/memory limit visibility and application readiness checks
- Container logs, browser terminal, host/container metrics, and image cleanup
- Local and S3-compatible backups with optional schedules
- Local users, roles, TOTP, and optional OAuth configuration
- Optional external-process plugins

Application readiness is configured with ordinary Compose labels:

```yaml
services:
  web:
    labels:
      windlass.health.url: https://app.example.com/health
      windlass.health.status: "200"
      windlass.health.contains: ready
      windlass.health.stability_seconds: "10"
```

## Install

```sh
curl -fsSL https://get.windlass.run | sudo sh
```

Claim the instance using the one-time token in `journalctl -u windlass`, then open
**Settings → Panel domain** after pointing a DNS record at the server. Caddy obtains and
renews the panel certificate automatically.

See [Installation](docs/install.md) for systemd, container, configuration, update, and
uninstall details.

## Development and verification

```sh
go test ./...
go vet ./...
cd web && npm ci && npm run build
go test -tags integration ./...   # Linux with Docker and Caddy
```

The production binary embeds `web/dist`:

```sh
CGO_ENABLED=0 go build -trimpath -tags embedweb ./cmd/windlass
```

## License

Apache-2.0
