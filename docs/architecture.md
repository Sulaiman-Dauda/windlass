# Architecture

One Go binary, one embedded React SPA, SQLite for metadata. Docker Compose
is the deployment engine, Caddy the router; Windlass orchestrates both and
invents nothing.

```
Browser ── React SPA (embedded, TanStack Query + SSE)
   │
   ▼  /api/v1 (chi, JWT-cookie sessions, RBAC)
Control plane ──────────────────────────────────────────┐
   │ services: projects · deploy · proxy · git ·        │
   │           backups · plugins · update               │
   │                                                    │ SQLite (WAL)
   ▼ internal/agent — THE privileged boundary           │ metadata only
┌─────────────────────────────────────────────┐         │
│ agent/local (the only pkg touching:)        │◄────────┘
│  · Docker SDK  · docker compose CLI         │
│  · Caddy admin API  · project filesystem    │
│  · git CLI  · container exec                │
└─────────────────────────────────────────────┘
```

## The agent boundary (principle 11)

Every privileged operation goes through the `agent.Agent` interface —
serializable types only, context everywhere, streaming as typed callbacks.
depguard fails CI if any other package imports the Docker SDK.

This is the future node-agent seam: replacing `agent/local` with a gRPC/mTLS
client turns Windlass multi-server without touching anything above the
interface. Tests run against `agent/fake` on any OS.

## The deploy pipeline (principle 13)

```
queued → preparing → syncing → pulling → building → applying → verifying
```

Jobs persist in SQLite with a step checkpoint written *before* each step.
Every step is idempotent (pinned git SHAs, convergent compose commands), so
a crash resumes by re-executing the checkpointed step at boot. Superseded
deployments cancel themselves; one active deployment per project; rollbacks
re-apply recorded image digests via a compose override file.

## Events

An in-process bus fans out structured events (principle 15) to SSE clients;
deployment events are *also* persisted per-deployment for lossless replay
(`Last-Event-ID`). The bus is lossy by design; the store is not.

## Caddy ownership (principle 6)

Windlass owns exactly one `@id`-tagged route object, applied with targeted
PATCHes — never `POST /load`. User-written Caddy config is never modified.
Routes re-sync on deployment completion and docker container events
(container IPs change on restart).

## Storage

- SQLite (`modernc.org/sqlite`, pure Go): users, sessions, deployments +
  events + artifacts, domains, git connections, backups, jobs, settings,
  audit log. Single connection, WAL.
- Disk: projects (source of truth), backups, key files.
- Secrets (env vars, tokens, TOTP seeds, S3/OAuth creds): AES-256-GCM with a
  key file, nonce-prefixed ciphertexts.

## RAM budget (principle 9)

No queues, no Prometheus, no background collectors. Log fan-out is
ring-buffered; container logs stream on demand only; metrics are read
straight from /proc and the Docker API when the dashboard asks. Idle target:
<40 MB RSS for the Go process, enforced by a CI check.

## Dependency policy (principle 10)

Direct dependencies: chi, Docker SDK, coder/websocket, modernc/sqlite,
x/crypto (argon2). Hand-rolled instead of imported: JWT (HMAC), TOTP
(RFC 6238), S3 SigV4 PUT/GET, sd_notify, OAuth flows, rate limiting,
host metrics, cron-lite scheduling.
