# Architecture

Windlass is one Go process with an embedded React SPA. Docker Compose is the deployment
engine, Caddy is the HTTP/TLS router, and SQLite stores control-plane state.

```text
Browser
  │ HTTPS / API / SSE / WebSocket
  ▼
Caddy ── user-owned configuration
  │      + windlass_panel_route
  │      + windlass_routes / windlass_route_* / windlass_https_redirect
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
- API tokens, as a SHA-256 hash of the secret part only;
- deployment/job state, events, image artifacts, backups, and schedules;
- encrypted Git connection, registry, S3 and OAuth credentials;
- which plugins are enabled;
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
and reruns the idempotent checkpoint.

A rollback is a new deployment. Pulled services are pinned to the target's recorded image
digests in a generated `compose.rollback.yaml`, passed with `-f` after the files a plain
deploy loads: those `COMPOSE_FILE` in `.env` lists, or else the project's compose file and
its override file, such as `compose.override.yaml`. Services with a `build:` key are left
out of the pin: on a Git project the target's commit is checked out in `src/` and rebuilt,
and without Git they keep their current image, with a warning in the log. The rollback
copies the target's artifacts, so it can be rolled back to in turn.

## Caddy ownership

Windlass uses targeted admin-API requests and never calls `POST /load`:

- `windlass_routes` is the application-domain subtree, placed first in the server that
  listens on `:443` or `:80`.
- `windlass_route_<hostname>` is an individual application route, dialling the running
  container's IP and port.
- `windlass_https_redirect` sends plain HTTP for Windlass hostnames to HTTPS.
- `windlass_panel_route` is the Settings-managed panel hostname.

Certificate automation names are merged with existing names and never removed. User routes
and unrelated Caddy configuration remain untouched. Routes are reconciled at startup and
after relevant Docker, deployment, project, or domain events. Every 30 seconds Windlass
also checks that `windlass_routes` still exists, and syncs when it does not, because a
Caddy reload loads the Caddyfile and drops every route the admin API added.

## Events and UI safety

An in-process event bus drives live invalidation. Deployment events are also persisted for SSE
replay with sequence numbers. Container logs stream on demand; the terminal alone uses a
WebSocket. A React route error boundary prevents a rendering failure from blanking the whole
application.

## Resource policy

Metrics are read on demand from `/proc` and Docker. There is no metrics collector, Redis, or
external queue. CI starts the production binary and enforces an idle Go-process RSS below
40 MiB. Caddy and the Docker socket proxy are separate processes and are measured separately.
