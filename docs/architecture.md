# Architecture

Windlass is one Go process with an embedded React SPA. Docker Compose is the deployment
engine, Caddy is the HTTP/TLS router, and SQLite stores control-plane state.

```text
Browser
  │ HTTPS / API / SSE / WebSocket
  ▼
Caddy ── user-owned configuration
  │      + windlass_panel_route
  │      + windlass_routes / windlass_route_*
  ▼
Windlass Go process
  ├── API, auth, audit, jobs, deploy, proxy, backup, plugin services
  ├── SQLite (platform state + rebuildable indexes/caches)
  └── internal/agent
       ├── docker compose CLI
       ├── Docker API through the restricted socket proxy
       ├── Caddy admin API
       ├── project filesystem and Git
       └── container exec and host metrics
```

## Application configuration ownership

The project directory is authoritative:

- `compose.yaml` or `compose.yml`: every Compose-defined setting, including services,
  images, ports, volumes, networks, environment references, restart policies, healthchecks,
  and CPU/memory limits.
- `.env`: standard dotenv values used by Compose. Windlass writes it atomically with mode
  `0600`, reads hand edits, and maintains an encrypted SQLite cache.
- `.windlass.json`: versioned Windlass manifest containing source type, Git repository and
  branch, auto-deploy choice, and domain mappings.

`projects.Service.Reconcile` scans directories containing a Compose file, creates a manifest
for installations that predate it, and upserts the project/domain index. Missing directories
are hidden rather than cascade-deleted so temporary mount failures do not erase platform
history. An explicitly requested project deletion removes both the directory and its platform
records.

## Platform storage

SQLite uses WAL mode and foreign keys. It stores:

- users, sessions, roles, TOTP/OAuth state, audit entries, and settings;
- deployment/job state, events, image artifacts, backups, and schedules;
- encrypted Git/S3/OAuth credentials;
- rebuildable project, domain, and environment indexes/caches.

The database does not contain Compose YAML or replace the project files. A new database can
rediscover applications, domains, Git source metadata, and `.env` values from disk. Accounts,
audit history, deployment history, backup records, and encrypted platform credentials require
a platform backup.

## Agent boundary and Docker access

All privileged operations use `agent.Agent` interfaces with serializable request/response
types. `agent/local` is the in-process implementation; `agent/fake` supports deterministic
unit tests.

The systemd installation does not grant direct `docker.sock` access to the Windlass user.
`windlass-docker-proxy.service` mounts the socket and exposes an API-allowlisted endpoint only
on `127.0.0.1:2375`. Windlass and the Compose CLI use `DOCKER_HOST` to reach it. Container
creation is still a privileged trust boundary, but unrelated Docker API families are blocked.

## Deployment chain

A deployment is a persisted SQLite job:

```text
queued → preparing → syncing → pulling → building → applying → verifying → succeeded
```

The relevant execution chain is:

```text
projects.RenderEnvFile       # reads/imports the authoritative .env
docker compose config
git sync                     # Git projects only
docker compose pull --ignore-buildable
docker compose build         # services with build contexts
docker compose up -d --quiet-pull --remove-orphans
docker compose ps -a --format json
HTTP application checks      # when windlass.health.* labels exist
```

Step checkpoints are recorded before execution. A restarted runner reclaims interrupted jobs
and reruns the idempotent checkpoint. Rollback deployments use recorded image digests in a
temporary Compose override.

## Caddy ownership

Windlass uses targeted admin-API requests and never calls `POST /load`:

- `windlass_routes` is the application-domain subtree.
- `windlass_route_<hostname>` is an individual application route.
- `windlass_panel_route` is the Settings-managed panel hostname.

Certificate automation names are merged with existing names. User routes and unrelated Caddy
configuration remain untouched. Routes are reconciled at startup and after relevant Docker,
deployment, project, or domain events.

## Events and UI safety

An in-process event bus drives live invalidation. Deployment events are also persisted for SSE
replay with sequence numbers. Container logs stream on demand; the terminal alone uses a
WebSocket. A React route error boundary prevents a rendering failure from blanking the whole
application.

## Resource policy

Metrics are read on demand from `/proc` and Docker. There is no metrics collector, Redis, or
external queue. CI starts the production binary and enforces an idle Go-process RSS below
40 MiB. Caddy and the Docker socket proxy are separate processes and are measured separately.
