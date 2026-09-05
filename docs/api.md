# API

Windlass has a versioned HTTP API under `/api/v1`. The panel is a client of it: there is
no private interface the UI uses and you cannot.

The full specification is served by a running instance and lives in the repository:

- `GET /api/v1/openapi.yaml` from your own panel
- [`api/openapi.yaml`](https://github.com/Sulaiman-Dauda/windlass/blob/main/api/openapi.yaml)

A test asserts the specification covers the routes that exist, so it does not drift away
from the implementation the way a hand-maintained document would.

## Authenticating

Sign in and keep the session cookie:

```sh
curl -sS -c jar -X POST https://panel.example/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"..."}'

curl -sS -b jar https://panel.example/api/v1/projects
```

Sessions are server-side rows, so a session can be revoked. Accounts with TOTP complete
the second factor before the session is usable.

## Shape of it

Sixty-six paths, grouped roughly as:

| Group | Examples |
| --- | --- |
| Projects | `GET/POST /projects`, `GET/PUT /projects/{name}/files/{path}`, `GET/PUT /projects/{name}/env` |
| Deployments | `GET/POST /projects/{name}/deployments`, `POST /projects/{name}/deployments/{number}/rollback` |
| Domains | `GET/POST /projects/{name}/domains`, `DELETE /projects/{name}/domains/{hostname}` |
| Backups | `GET/POST /projects/{name}/backups`, `POST /projects/{name}/backups/{id}/restore`, `GET/PUT /projects/{name}/backups/schedule` |
| Runtime | `GET /projects/{name}/services`, `GET /projects/{name}/logs`, `GET /projects/{name}/terminal` |
| Platform | `GET /system/metrics`, `GET/POST /system/update`, `GET /proxy/status`, `GET /events` |
| Access | `GET/POST /users`, `PUT /users/{id}/role`, `POST /auth/totp/setup` |
| Git | `GET/POST /git/connections`, `PUT /projects/{name}/git`, `POST /webhooks/{provider}/{project}` |
| Templates | `GET /templates`, `POST /templates/{key}` |

## Streaming

Logs, deployment progress and platform events are Server-Sent Events. `GET /events` is the
platform stream; `GET /projects/{name}/deployments/{number}/events` follows one deployment.

An SSE connection stays open, which trips up tooling that waits for a request to finish.
Consume it as a stream or ask for a bounded read.

`GET /projects/{name}/terminal` is a WebSocket.

## Rules that apply to every endpoint

State-changing requests are checked against the caller's role and written to the audit
log. The check is in the handler, so it applies equally to the panel, to `curl`, and to
anything else holding a session.

Rate limiting applies to authentication endpoints.

## Health

`GET /api/v1/system/health` needs no authentication and is the right target for an uptime
check.
