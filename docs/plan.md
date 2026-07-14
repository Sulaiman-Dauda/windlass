# Windlass implementation record

This document records the product that exists now. It is not a speculative roadmap; behavior
described here must stay synchronized with code, OpenAPI, tests, and operator documentation.

## Product boundaries

Windlass is a single-server Docker Compose control plane. It deliberately does not implement
a proprietary scheduler or container specification. Its responsibilities are:

- authenticate operators and provide an audited UI/API;
- maintain plain project directories and a rebuildable application index;
- run resumable `docker compose` deployments;
- maintain targeted Caddy routes and certificate automation;
- expose logs, terminal access, metrics, backups, templates, Git/webhooks, updates, and
  optional external-process plugins.

Multi-node execution is not implemented. The internal agent interface is a seam for a future
remote agent, but the current implementation is in-process only.

## Non-negotiable behavior

1. `compose.yaml`, `.env`, and `.windlass.json` are authoritative application configuration.
2. SQLite contains platform state and rebuildable indexes/caches, never Compose YAML.
3. Windlass executes the Compose CLI rather than building container specs through the SDK.
4. Stopping Windlass never stops application containers.
5. Caddy updates are limited to stable Windlass-owned `@id` objects.
6. The systemd service has no direct Docker socket access and uses an API-allowlisted proxy.
7. Deployment jobs are checkpointed, idempotent, and reclaimable after process interruption.
8. Optional features must not require Redis, Kubernetes, Swarm, Prometheus, or another queue.
9. The Go process must remain below the CI 40 MiB idle-RSS budget.

## Implemented stack

- Go 1.26, chi, sqlc-generated queries, pure-Go `modernc.org/sqlite`, Moby client packages,
  `coder/websocket`, and standard-library HTTP/SSE handling.
- React 18, TypeScript, Vite, Tailwind, TanStack Query, React Router, and xterm.js.
- Embedded production SPA selected by the `embedweb` build tag.
- Caddy admin API for routing/TLS and Docker Compose v2 for application lifecycle.

## Persistent model

### Project files

```text
projects/<name>/
├── compose.yaml or compose.yml
├── .env
└── .windlass.json
```

The manifest schema is versioned and currently records `source`, `git_repo`, `git_branch`,
`auto_deploy`, and domain mappings. The scanner imports existing stacks and synthesizes a
manifest from legacy SQLite rows when needed.

### SQLite tables

The migrations define platform tables for users, sessions, audit entries, settings, projects,
environment cache rows, deployments, deployment artifacts/events, jobs, domains, Git
connections, backups/schedules, and plugin enablement. Project/domain/environment rows can be
rebuilt from files; user and historical/platform records cannot.

Secrets stored as platform state use AES-256-GCM with `data/secret.key`. Session signing uses
`data/session.key`. `.env` remains plaintext because Docker Compose consumes it directly and is
protected with mode `0600`.

## Deployment state machine

```text
queued → preparing → syncing → pulling → building → applying → verifying → succeeded
```

- Preparing imports `.env`, rejects obvious placeholder secrets, and runs `compose config`.
- Syncing updates Git projects to the requested commit.
- Pulling/building calls the Compose CLI and records resolved image digests.
- Applying calls `docker compose up -d --quiet-pull --remove-orphans`.
- Verifying polls `compose ps`; Docker unhealthy/non-zero exits fail immediately. Optional
  `windlass.health.*` labels add HTTP status/body/stability checks.
- Rollback deployments use recorded image digests in `compose.rollback.yaml`.

The SQLite job runner writes checkpoints before steps, reclaims interrupted jobs, and prevents
two active deployments for the same project.

## Routing model

- `windlass_routes`: application route subtree.
- `windlass_route_<hostname>`: individual reverse-proxy route to a live Compose container IP.
- `windlass_panel_route`: Settings-managed route to `WINDLASS_PANEL_UPSTREAM`.

All updates use targeted Caddy admin paths. Certificate automation hostnames are merged, not
replaced. Desired state is reapplied at startup and after relevant runtime events. Runtime-only
routes do not survive a Caddy restart while Windlass is permanently absent; the Caddyfile is
the persistent administrator-owned fallback.

## API and UI

All APIs use `/api/v1`; `api/openapi.yaml` is served by the application and a route-coverage
test fails when a handler path is undocumented.

Implemented UI areas are dashboard, projects, project overview/deployments/domains/Git/files/
environment/logs/terminal/backups, templates, and settings for panel domain, TOTP, Git
connections, users, Docker image storage, and updates. Plugin lifecycle and some platform
configuration are API-driven rather than presented as dedicated Settings sections.

The Environment tab accepts individual entries or bulk dotenv paste. The Files tab is a raw
text editor: it sends the edited bytes directly and therefore does not parse/reformat YAML.

## Security boundaries

- Local password authentication, revocable sessions, RBAC, TOTP, and optional GitHub/Google
  OAuth are implemented.
- Security headers, login rate limiting, encrypted platform credentials, audit entries, path
  traversal protection, and role checks cover the web/control plane.
- The Docker proxy blocks API families not allowlisted, but Compose container creation remains
  a root-equivalent host trust boundary. Keep Windlass authenticated and private.
- Caddy and Docker admin endpoints must remain loopback/private-network only.

## Verification gates

- `go test ./...` and `go vet ./...`
- frontend TypeScript production build
- OpenAPI route coverage
- Linux integration tests with real Docker and Caddy
- trusted-HTTPS nginx routing while preserving a user-owned Caddy route
- fresh-SQLite filesystem reconstruction while the Compose stack remains running
- Playwright first-run → project → real deploy → service state → sign-out
- installer test for service health, Docker proxy allowlisting, and direct-socket denial
- production-binary idle-RSS check below 40 MiB

## Known operational limitations

- Files are read on screen load/deploy/scan; there is no continuous filesystem watcher.
- The panel-domain feature cannot create DNS records. DNS must point at the server first.
- Container installation does not bundle Caddy; operators must provide reachable Caddy admin
  and panel-upstream addresses.
- Deleting SQLite loses platform accounts/history/settings even though applications can be
  rediscovered.
- Application route persistence across a Caddy restart depends on Windlass being available to
  reconcile, unless equivalent routes are also maintained in the administrator's Caddyfile.
