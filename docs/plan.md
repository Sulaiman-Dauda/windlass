# Windlass — Lightweight Docker Compose Control Plane (full build plan)

## Context

The user runs Coolify on a VPS and finds it too heavy (~800MB–2GB idle, 15–30 containers). After researching alternatives (Dokploy, CapRover, Dokku, Dockge, Kamal) they spec'd a new product: a **lightweight, self-hosted "Docker Compose control plane"** — Coolify's core value (Git deploys, domains + auto-HTTPS, one-click databases, logs, env management) at <80MB idle RAM, built so that **if the panel is deleted, every deployed app keeps running** on plain Docker Compose + Caddy.

This plan builds the product **end to end (all 10 deliverables)**, executed as ~12 sequential production-quality milestones with tests — per the user's explicit choices.

## User-locked decisions

| Decision | Choice |
|---|---|
| Scope | Everything: backend, frontend, DB schema, Docker image, installer, auto-update, CI/CD, OpenAPI docs, plugin SDK, docs |
| Execution | Milestone-by-milestone with tests per subsystem, not one monolithic pass |
| Architecture | **Single Go binary, agent-ready**: all privileged ops behind an internal `agent` interface; in-process now, splittable into an mTLS node-agent later |
| Local toolchain | **Go only** (install via winget). Unit tests mock the agent; real Docker/Caddy integration tests run in GitHub Actions ubuntu runners |
| Name | **Windlass** (researched — below) |

## Product name: Windlass

A windlass is the ship's winch that does the heavy lifting — raising the anchor, hauling mooring lines. Fits the product, short, easy to spell, brandable.

- **Conflict check (2026-07)**: no software/DevOps/GitHub project of note named "windlass". Binary/CLI: `windlass` · module: `github.com/windlass-dev/windlass` (org TBD at repo-publish time) · data dir: `/var/lib/windlass`.
- Runner-ups (also clean): Moorage, Homeport, Fairlead. Rejected for conflicts: Slipway (existing Caddy-based deploy platform), Dockhand + Hawser (existing panel + agent), Cleat (cleat.sh AI sandbox), Gantry, Berth, Stevedore, Bowline.

## Non-negotiable principles (verbatim from user — govern every milestone)

1. Docker Compose is the source of truth.
2. The platform is stateless except for metadata.
3. Every feature must work if accessed directly from Linux.
4. No proprietary deployment formats.
5. No unnecessary background services.
6. Everything should be replaceable by standard Linux tools.
7. If the panel is removed, every deployed application must continue running.
8. Prefer deleting code over adding abstractions.
9. New features must not increase idle RAM by more than 5 MB without compelling reason.
10. Every dependency must justify its existence.
11. Every privileged operation must go through the (internal, future-splittable) Node Agent boundary.
12. Control Plane ↔ Node Agent communication uses mTLS with certificate rotation (applies when the agent splits out; the interface is designed for it now).
13. Every deployment is idempotent and resumable after interruption.
14. All APIs are versioned from day one.
15. Every operation emits structured events for the UI and audit log.

## Tech stack (locked)

- **Backend**: Go 1.24.x, chi, sqlc, SQLite WAL (**`modernc.org/sqlite`** — pure Go, CGO-free ⇒ painless cross-compile from Windows), Docker SDK, Caddy admin API, slog, graceful shutdown, no global state
- **Frontend**: React + Vite + TypeScript, Tailwind, shadcn/ui, TanStack Query, React Router, xterm.js, CodeMirror 6 (lighter than Monaco) — static build embedded via `go:embed`. One binary total. Node 22 used at build time only.
- **Realtime**: SSE for deploys/logs/events (replayable via `Last-Event-ID`); WebSocket (`coder/websocket`) only for the terminal
- **Auth**: argon2id local users, JWT in HTTP-only cookies (carrying a revocable session id), RBAC (`admin|member|viewer`), optional TOTP, GitHub + Google OAuth
- **Proxy**: Caddy (auto Let's Encrypt) via admin API, zero-downtime targeted updates, graceful degradation banner if absent
- **Zero**: Redis, queues, Kafka, Prometheus, Grafana, K8s, Swarm, ORM, gopsutil (read `/proc` directly), aws-sdk (hand-rolled S3 SigV4, ~200 LOC)
- **Compose execution: shell out to `docker compose` CLI** (not SDK reimplementation) — identical behavior to what a user runs by hand (principles 1/6/8); machine-read via `--format json`; require compose v2.24+

## Repository layout

```
windlass/
├── cmd/windlass/main.go        # entrypoint: flags, wiring, run
├── internal/
│   ├── agent/                  # THE privileged boundary
│   │   ├── agent.go            # interfaces + serializable req/resp types
│   │   ├── local/              # in-process impl (Docker SDK, compose CLI, Caddy, FS)
│   │   ├── fake/               # deterministic fake for unit tests (Windows-safe)
│   │   └── proto/              # reserved for future mTLS wire types
│   ├── server/                 # chi router, middleware, SSE/WS plumbing, embedded web/dist
│   ├── api/                    # /api/v1 handlers, validation, DTOs (thin)
│   ├── auth/                   # argon2id, JWT cookies, TOTP, OAuth, RBAC
│   ├── store/                  # sqlc queries + embedded migrations runner
│   ├── projects/               # project dir lifecycle: compose.yaml + .env on disk
│   ├── deploy/                 # deployment state machine + rollback
│   ├── jobs/                   # SQLite-persisted in-process job runner (resumable)
│   ├── events/                 # event bus → SSE fan-out + audit sink
│   ├── proxy/                  # Caddy config generation, ownership, degrade
│   ├── git/                    # clone/pull, GitHub/GitLab APIs, webhook HMAC
│   ├── secrets/                # AES-256-GCM env encryption, key file
│   ├── dbtemplates/            # one-click postgres/redis/mysql/mongo compose generators
│   ├── backups/                # manual + cron, local + S3 (SigV4)
│   ├── metrics/                # Docker stats + /proc host metrics, on-demand
│   ├── plugins/                # external-process plugin discovery/lifecycle/proxy
│   ├── update/                 # self-update: download, verify, atomic replace
│   ├── terminal/               # WS ↔ agent exec bridge
│   └── version/
├── migrations/                 # 0001_init.sql… forward-only, embedded
├── web/                        # React+Vite app → dist/ embedded
├── install/                    # install.sh, windlass.service, docker-compose install
├── plugins-sdk/                # manifest spec + example plugin + docs
├── docs/                       # markdown docs (install, architecture, "life without the panel")
├── api/openapi.yaml            # hand-maintained OpenAPI 3.1
├── .github/workflows/{ci.yaml,release.yaml}
├── Dockerfile, Makefile, sqlc.yaml, go.mod
```

Dependency direction: `api → services → agent + store`. Only `internal/agent/local` may import the Docker SDK or touch Caddy/FS — **enforced by depguard in golangci-lint**.

## The agent interface (heart of the codebase)

Rules that keep it mTLS-splittable: every method takes `context.Context`; params/results are plain serializable structs (no `io.Writer`, no Docker SDK types leaking); streaming = typed serializable events via `LogSink func(LogLine)` callbacks; long ops cancellable. Maps 1:1 to gRPC unary + server-streaming later.

```go
type Agent interface {
    Compose() ComposeAgent   // Up/Down/Pull/Build/PS/Config — via docker compose CLI
    Docker() DockerAgent     // ListContainers/Logs/Stats/Inspect/ImageTag/ImageDigest/Prune/Events
    Proxy() ProxyAgent       // Available/ApplyRoutes(desired-state)/CurrentRoutes
    FS() FSAgent             // Read/Write(atomic tmp+rename)/List/EnsureProject/RemoveProject — scoped to /var/lib/windlass/projects, path-traversal-proof
    Exec() ExecAgent         // Start(ExecReq) → ExecSession{Write,Resize,Output <-chan []byte,Wait,Close} — only bidirectional stream (terminal)
    Host() HostAgent         // Metrics (/proc + statfs), GitSync (clone/pull with deploy key)
    Ping(ctx) (NodeInfo, error)  // versions, docker/caddy availability
}
```

v1 wires `agent/local.New(cfg)`. Future split: `agent/remote` (gRPC + mTLS client) + `windlass agent` subcommand serving `local` — zero changes above the interface.

## SQLite schema (WAL, foreign_keys=ON, single-writer discipline)

- **users**: email UNIQUE, password_hash (argon2id, NULL if OAuth-only), role, totp_secret_enc, oauth_provider/subject, disabled_at
- **sessions**: token-hash id, user_id, expires_at, ip, user_agent (JWT carries session id → revocable)
- **projects**: name UNIQUE (= dir name = compose project name), source (`git|manual|template`), git_repo/branch, auto_deploy. *No compose content — disk is truth.*
- **env_vars**: project_id, key, value_enc (AES-256-GCM nonce-prefixed), UNIQUE(project_id,key). Rendered to `.env` at deploy; hand-edits re-imported on drift detection.
- **deployments**: project_id, per-project seq number, status, trigger (`manual|webhook|rollback|schedule`), git_commit, error, rollback_of
- **deployment_artifacts**: deployment_id, service, image_ref, image_digest → enables rollback retag
- **deployment_events**: deployment_id, seq, ts, type, message, data — replayable SSE catch-up
- **domains**: project_id, hostname UNIQUE, service, container_port, tls, status
- **git_connections**: provider, token_enc, webhook_secret_enc
- **jobs**: type, payload, status (`queued|running|done|failed|dead`), step (resume checkpoint), attempts, run_after, locked_at
- **backups** + **backup_schedules** (cron_expr, destination JSON w/ encrypted S3 creds, retention)
- **settings** (key/value JSON), **audit_log** (append-only), **plugin_installs**
- Migrations: numbered forward-only SQL, embedded, transactional, tracked in `schema_migrations`

## Deployment pipeline state machine

`queued → preparing → syncing → pulling → building → applying → verifying → succeeded`, with `failed`/`cancelled` from any active state; `rolled_back` as terminal annotation.

Steps = persisted `jobs.step` checkpoints (written before execution, marked after):
1. **preparing** — snapshot env → write `.env` atomically; `compose config` validates; record image refs
2. **syncing** — git checkout to pinned SHA (skipped for manual projects)
3. **pulling/building** — `compose pull` / `compose build`; persist image digests to deployment_artifacts
4. **applying** — `compose up -d --remove-orphans` (convergent, idempotent)
5. **verifying** — poll `compose ps` + healthchecks until healthy or 120s timeout; then apply Caddy routes

**Resume after crash**: startup reclaims `running` jobs with stale `locked_at`; every step is idempotent so resume = re-execute checkpointed step. Reclaimed jobs re-check they're still the project's latest deployment before resuming.
**Rollback**: new deployment with `rollback_of=N` — git projects check out N's commit; image projects retag recorded digests via `compose.rollback.yaml` override, then `up -d`. Last K deployments' artifacts protected from prune.
**Concurrency**: one active deployment per project (partial UNIQUE index); webhook deploys for same project debounce/collapse to latest.

## API surface (`/api/v1`)

- **auth**: login/logout, totp setup/verify, oauth start/callback, me
- **users** (admin): CRUD + roles
- **projects**: CRUD, `files/{path}` GET/PUT (compose editing), `env` GET/PUT, actions start/stop/restart
- **deployments**: create (deploy), list, get, rollback, cancel
- **domains**: CRUD per project; `GET /proxy/status`
- **git**: connections CRUD; `POST /webhooks/{provider}/{project}` (public, HMAC-verified)
- **templates**: list; `POST /templates/{postgres|redis|mysql|mongodb}` → creates project
- **system**: metrics, info, update, health (public)
- **backups**: CRUD, restore, schedules
- **plugins**: list/install/enable; `ANY /plugins/{name}/proxy/*`
- **Streaming**: SSE `GET /events?topics=` (global bus → invalidations), SSE deployment events (replay + live via `Last-Event-ID`), SSE container logs; WS terminal only
- Errors `{error:{code,message}}`; OpenAPI 3.1 hand-written, served + static viewer; CI route-coverage check (chi routes vs spec paths)

## Frontend structure

Routes: `/login`, `/` (dashboard: host metrics, project cards, recent deploys), `/projects/:name` tabs — Overview, Deployments (live log view), Files (CodeMirror), Env, Domains, Logs, Terminal, Backups; `/templates`, `/settings/{users,git,system,plugins,backups}`.

SSE ↔ TanStack Query: one `useEventSource` hook; global `/events` maps event types → `queryClient.invalidateQueries` so views stay live without polling; deploy-log streams bypass Query (local ring buffer — logs aren't cache-shaped). Dev mode: Vite proxies `/api` to Go binary.

## Milestones (~12, each shippable + tested)

0. **Toolchain**: install Go via winget on this machine; git init the repo.
1. **Scaffold + CI skeleton** — Go module, chi + `/system/health`, slog, graceful shutdown, config; Vite app embedded and served; Makefile (`make dev/build` works on Windows sans Docker); CI: vet, golangci-lint, unit tests, frontend build, cross-compile linux/amd64+arm64. *Done: one binary serves SPA + health; CI green.*
2. **Store + auth + audit** — migrations, sqlc, users/sessions/audit, argon2id, JWT cookies, RBAC, first-run admin bootstrap token, login UI + app shell. *Done: auth unit tests; browser login works.*
3. **Agent interface + fake + local skeleton** — full interface, scriptable `fake`, `local` (Docker/FS/Host), depguard rule. *Done: unit tests on Windows via fake; first build-tagged integration test lists containers on ubuntu CI.*
4. **Projects on disk + files UI** — CRUD, compose.yaml/.env editing, `compose config` validation, env encryption + key file. *Done: full project lifecycle from UI; path-traversal tests.*
5. **Deploy pipeline v1 (manual)** — jobs runner, state machine, ComposeAgent, deployment_events, SSE deploy logs, Deployments UI. *Done: crash/resume matrix tests vs fake; CI deploys real nginx project end-to-end.*
6. **Domains + Caddy** — ProxyAgent desired-state under `windlass_*` @id-tagged subtree, degrade banner, Domains UI. *Done: CI curl-through-Caddy test; panel restart never clobbers user's own Caddy routes.*
7. **Git + webhooks + rollback** — connections, deploy keys, HMAC webhooks, auto-deploy, digest tracking, rollback UI. *Done: HMAC unit tests; CI webhook→deploy; rollback restores previous digest.*
8. **Templates + logs + terminal + metrics** — one-click DBs (generated passwords into env_vars), log SSE, xterm WS terminal, dashboard metrics. *Done: CI creates + connects to Postgres; WS echo test.*
9. **Backups** — project-dir tar + DB-native dumps via exec, local + S3 SigV4, cron schedules, restore. *Done: CI round-trip.*
10. **Installer + Docker image + auto-update** — install.sh (distro detect, Docker/Caddy install, systemd, bootstrap URL), Dockerfile, release workflow (checksummed signed binaries + GHCR), atomic self-update. *Done: CI installer test on bare ubuntu; vN-1→vN update smoke; VPS smoke-test doc.*
11. **Plugin SDK + OpenAPI polish** — `plugin.json` manifest, external-process lifecycle (spawned only when enabled ⇒ zero RAM idle), proxy route, example plugin; finalize spec + viewer + route-coverage check. *Done: example plugin installs/serves UI tab/uninstalls.*
12. **Hardening + docs + 1.0** — README + docs set (incl. "life without the panel" per principle 7), auth rate limiting, `/security-review` pass, idle-RSS budget check in CI (<40MB Go process), one Playwright critical-path smoke (login→create→deploy→logs→domain). *Done: perf budget green; docs complete.*

## Testing strategy

- **Unit (Windows-safe, every milestone)**: services vs `agent/fake`; state-machine crash simulation; AES-GCM known-answer tests; Caddy config golden JSON; webhook HMAC
- **Integration (`//go:build integration`, ubuntu CI)**: real Docker + Caddy, `agent/local` + full API via httptest; serialized, namespaced, torn down
- **E2E**: exactly one Playwright smoke spec (SSE/WS/xterm are where API tests lie) — capped to limit maintenance
- **CI**: job1 lint+unit → job2 frontend build/typecheck/vitest → job3 integration (Docker + Caddy) → job4 Playwright. Release: tag → CGO-free cross-compile → checksums → Release + GHCR image
- **Frontend embed**: `web/dist` never committed; placeholder `dist/index.html` behind a `dev` build tag so `go test ./...` needs no Node — established in milestone 1

## Installer + auto-update

- **install.sh**: root check → distro detect → Docker via get.docker.com if missing (prompt unless `--yes`) → Caddy from official repo → `windlass` system user (docker group) → `/var/lib/windlass/{projects,data,backups}` → download binary, sha256 verify → systemd unit → start → print one-time bootstrap URL. Flags: `--version --no-caddy --yes`. Also: docker-compose method (self-update disabled there) and bare binary.
- **systemd**: `Type=notify` (sd_notify ≈30 LOC, no dep), `Restart=on-failure`, `NoNewPrivileges=yes`, `ReadWritePaths=/var/lib/windlass`
- **Self-update**: GitHub Releases check → download → sha256 + minisign verify → atomic `rename(2)` over binary → drain jobs → exit; systemd restarts into new binary → migrations run; crash-loop leaves `windlass.previous` for one-command rollback. App containers never touched.

## Key risks (top calls already locked)

1. **Compose via CLI** (not SDK reimplementation) — identical-by-construction behavior; parse only `--format json` outputs
2. **Caddy ownership** — only touch `windlass_*` @id objects via targeted PUTs, never `POST /load`; diff-and-warn on drift; golden-file tests
3. **Secret key** — 32-byte key at `data/secret.key` (0600); included in platform backups but archive is passphrase-encrypted (scrypt); documented loudly
4. **SSE through proxies** — flush per event, `flush_interval -1` on panel's own Caddy route, 15s heartbeats, per-user stream caps, lossless reconnect via event replay
5. **RAM budget** — ring-buffered log fan-out (1000 lines/stream), on-demand log streaming only, CI idle-RSS assertion
6. **Resume edge cases** — explicit crash-matrix tests in milestone 5, not later bug fixes

## Verification (end-to-end)

- Every milestone: `go test ./...` green on Windows (fake agent) + CI integration suite green on ubuntu (real Docker + Caddy)
- Pipeline proof: CI test that kills the process mid-deploy and asserts convergence after restart (principle 13)
- Principle 7 proof: CI test that stops the panel and asserts the deployed nginx app still serves through Caddy
- Perf: CI asserts idle RSS < 40MB for the Go process; startup < 500ms
- Final: user deploys to a real VPS via `curl | sh` installer and runs the documented smoke checklist (create project → domain → HTTPS → webhook deploy → rollback)
