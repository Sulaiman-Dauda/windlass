# Windlass

A lightweight, self-hosted Docker Compose control plane (Coolify alternative). Single Go binary with an embedded React frontend; Caddy for routing/TLS; SQLite for metadata only.

## Non-negotiable principles

1. Docker Compose is the source of truth — projects are plain directories (`compose.yaml` + `.env`) a user can edit by hand.
2. If the panel is removed, every deployed app keeps running.
3. Every privileged operation (Docker, Caddy, project FS, exec) goes through `internal/agent` — the future mTLS node-agent boundary. Only `internal/agent/local` may import the Docker SDK (enforced by depguard).
4. SQLite stores metadata only, never application state. No Redis/queues/Prometheus/K8s. Minimal dependencies — each one must justify itself.
5. Idle RAM budget: <40MB for the Go process. Deployments are idempotent and resumable.
6. All APIs live under `/api/v1`. Every operation emits structured events (SSE) and audit-log entries.

## Build & test

- `go test ./...` — unit tests, run anywhere (Windows-safe; uses `internal/agent/fake`). Never requires Node or Docker.
- `go build ./cmd/windlass` — dev binary with a placeholder frontend (no Node needed).
- `cd web && npm run build` then `go build -tags embedweb ./cmd/windlass` — production binary with embedded SPA.
- `go test -tags integration ./...` — real Docker + Caddy; Linux/CI only, never on this Windows dev machine.
- Frontend dev: `npm run dev` in web/ (proxies `/api` to :8080) alongside `go run ./cmd/windlass`.

### Windows dev machine notes

- Go lives at `%LOCALAPPDATA%\go-toolchain\go\bin\go.exe` (zip install, no admin). If `go` is not on PATH, use the full path.
- Docker is NOT available locally — anything touching `agent/local` Docker/Caddy code is compile-checked here, behavior-tested in CI.

## Architecture map

- `cmd/windlass` — entrypoint, wiring
- `internal/agent` — privileged-op interfaces (serializable types only; no Docker SDK types leak out); `local/` real impl, `fake/` for unit tests
- `internal/server` — chi router, middleware, SSE/WS plumbing, SPA serving
- `internal/api` — thin /api/v1 handlers → services
- `internal/{projects,deploy,jobs,events,proxy,git,secrets,dbtemplates,backups,metrics,plugins,update,terminal}` — one service package per subsystem
- `internal/store` — sqlc queries + embedded migrations; `migrations/*.sql` forward-only
- `web/` — React+Vite+Tailwind SPA, embedded via `web/embed.go` behind the `embedweb` build tag (stub otherwise)

The full build plan with milestone acceptance criteria lives in `docs/plan.md`.
