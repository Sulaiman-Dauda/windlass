# Windlass contributor guide

Windlass is a single-Go-process Docker Compose control plane with an embedded React SPA,
Caddy integration, and SQLite platform storage.

## Architectural rules

1. The project filesystem is authoritative for application configuration. Compose owns
   service configuration; `.env` owns environment values; `.windlass.json` owns the small
   amount of Windlass-specific application configuration.
2. SQLite stores platform state and rebuildable indexes/caches. Never make a running
   application depend on a database-only copy of Compose configuration.
3. Deployed containers must continue running if Windlass stops or is removed.
4. Privileged operations go through `internal/agent`. Only `internal/agent/local` imports
   Docker/Moby packages or directly accesses Docker, Caddy, project files, Git, or exec.
5. Compose operations shell out to `docker compose`; do not reconstruct container specs.
6. Caddy changes use targeted `@id` operations. Windlass owns `windlass_routes`,
   `windlass_panel_route`, and their child `windlass_route_*` objects only. Never load or
   replace the complete Caddy configuration.
7. The installed service reaches Docker through the restricted loopback socket proxy. Do
   not add the `windlass` user to the `docker` group.
8. Keep the Go process below the 40 MiB CI idle-RSS budget and avoid always-on dependencies.
9. APIs are versioned under `/api/v1`; state-changing handlers require the appropriate role
   and write audit entries.

## Source tree

- `cmd/windlass`: configuration, dependency wiring, lifecycle
- `internal/agent`: privileged-operation interfaces; `local` and deterministic `fake`
- `internal/projects`: filesystem discovery, manifests, files, and `.env` synchronization
- `internal/deploy`: resumable Compose deployment state machine
- `internal/proxy`: domain desired state and Caddy reconciliation
- `internal/store`: SQLite setup, generated sqlc queries, and custom rebuild helpers
- `internal/server` and `internal/api`: routing, middleware, streaming, and thin handlers
- `web`: React/Vite frontend embedded with the `embedweb` tag
- `migrations`: forward-only embedded SQLite migrations
- `install`: systemd and Docker Compose installation methods

## Build and test

```sh
go test ./...
go vet ./...
cd web && npm run build
go test -tags integration ./...  # Linux, Docker, and Caddy required
```

For a production binary, build the frontend first and then use:

```sh
CGO_ENABLED=0 go build -trimpath -tags embedweb ./cmd/windlass
```

Unit tests use `internal/agent/fake` and run without Docker. The integration suite deploys
real Compose stacks, exercises Caddy over trusted HTTPS, checks user-owned route preservation,
and proves a fresh SQLite database can rebuild its application index while containers remain
running.
