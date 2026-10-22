# API

Windlass has a versioned HTTP API under `/api/v1`. The panel is a client of it: there is
no private interface the UI uses and you cannot.

The full specification is served by a running instance and lives in the repository:

- `GET /api/v1/openapi.yaml` from your own panel
- [`api/openapi.yaml`](https://github.com/Sulaiman-Dauda/windlass/blob/main/api/openapi.yaml)

A test asserts the specification covers the routes that exist, so it does not drift away
from the implementation the way a hand-maintained document would.

## Authenticating

Scripts, CI jobs and AI agents use an API token. The panel itself uses a session cookie.

### API tokens

An admin creates a token under **Settings, API tokens**. It is shown once, so store it
straight away in a password manager or secret store. Send it as a bearer token:

```sh
export WINDLASS_TOKEN=wl_pat_...   # from your secret store, not typed into history
curl -sS -H "Authorization: Bearer $WINDLASS_TOKEN" https://panel.example/api/v1/projects
curl -sS -H "Authorization: Bearer $WINDLASS_TOKEN" https://panel.example/api/v1/auth/tokens/current
```

A token acts as the person who made it, limited to the access chosen when it was created:

| Access | Acts as | Can |
| --- | --- | --- |
| Read only | `viewer` | Read projects, deployments, logs and metrics |
| Deploy and edit | `member` | Also create projects, edit compose and env, deploy, roll back, manage domains and backups |
| Admin | `admin` | Also registries, Git connections, reading the S3 settings, and image pruning |

The limit is worked out on every request against the owner's current role, so demoting the
owner reduces what their tokens can do at once, and deleting the owner deletes their
tokens.

Some things a token can never do, whatever its access. They need a person signed in to the
panel, and the API answers `403 session_required`:

- create, list or revoke tokens, and manage users or roles
- set up or remove two-factor authentication, or configure sign-in providers
- change the panel domain, apply an update, change the S3 backup destination
- open a terminal
- delete a project, or restore a backup over it

Env values come back masked (`********`) unless the token was created with **Can read env
values**. Leave it off unless the job has to read secrets. A token without it can still
send the masked map back to `PUT /projects/{name}/env` with one value changed: a key whose
value is still `********` keeps what is stored. Writing a `.env` file that still holds
masked values is refused with `400 masked_values`. Compose files are masked the same
way: inline `environment:`, build `args:` and config `content:` values read as
`********`, and writing a YAML file that still holds them is refused.

The flag fully protects secrets only on a read-only token. A Deploy and edit token can
change compose and deploy, and a container it starts can print its environment to the
logs. Treat any write token as able to reach the project's secrets, and store it as
carefully as the secrets themselves.

Every token expires, after 90 days by default and at most 365. Tokens can be revoked at
any time in Settings, and an admin can see and revoke anyone's under
`GET /users/{id}/tokens`. The audit log records which token made each change.
Signing out does not end tokens. If an account may have been compromised, revoke its
tokens in Settings as well.

Tokens start with `wl_pat_` so secret scanners and people recognise one that has leaked.
Windlass stores only a SHA-256 hash of the secret part.

### Behind Cloudflare

If the panel's hostname is proxied through Cloudflare with a bot challenge, Cloudflare
answers API requests with its own 403 page before they reach Windlass, because a script
cannot solve a challenge.

With Super Bot Fight Mode (Pro plans and above), add a WAF custom rule with the Skip
action for Super Bot Fight Mode, matching requests whose path starts with `/api/` and
that carry an `Authorization` header. The free plan's Bot Fight Mode cannot be skipped by
any rule, so the API only works with it turned off. Windlass still checks every token.

### Sessions

The panel signs in and keeps a session cookie:

```sh
curl -sS -c jar -X POST https://panel.example/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"..."}'

curl -sS -b jar https://panel.example/api/v1/projects
```

Sessions are server-side rows, so a session can be revoked. Accounts with TOTP send
`totp_code` with the password. An account with TOTP that signs in through GitHub or Google
is sent back to the panel with a five-minute ticket and finishes at
`POST /auth/oauth/totp`.

## Shape of it

Seventy-three paths, grouped roughly as:

| Group | Examples |
| --- | --- |
| Projects | `GET/POST /projects`, `GET/PUT /projects/{name}/files/{path}`, `GET/PUT /projects/{name}/env` |
| Deployments | `GET/POST /projects/{name}/deployments`, `POST /projects/{name}/deployments/{number}/rollback` |
| Domains | `GET/POST /projects/{name}/domains`, `DELETE /projects/{name}/domains/{hostname}` |
| Backups | `GET/POST /projects/{name}/backups`, `POST /projects/{name}/backups/{id}/restore`, `GET/PUT /projects/{name}/backups/schedule` |
| Runtime | `GET /projects/{name}/services`, `GET /projects/{name}/logs`, `GET /projects/{name}/terminal` |
| Platform | `GET /system/metrics`, `GET/POST /system/update`, `GET /proxy/status`, `GET /events` |
| Access | `GET/POST /users`, `PUT /users/{id}/role`, `POST /auth/totp/setup`, `GET/POST /auth/tokens` |
| Git | `GET/POST /git/connections`, `PUT /projects/{name}/git`, `POST /webhooks/{provider}/{project}` |
| Templates | `GET /templates`, `POST /templates/{key}` |
| Plugins | `GET /plugins`, `POST /plugins/{name}/enable`, `/plugins/{name}/proxy/{path}` |

## Streaming

Logs, deployment progress and platform events are Server-Sent Events. `GET /events` is the
platform stream; `GET /projects/{name}/deployments/{number}/events` follows one deployment.

An SSE connection stays open, which trips up tooling that waits for a request to finish.
Consume it as a stream or ask for a bounded read.

`GET /projects/{name}/terminal` is a WebSocket.

## Rules that apply to every endpoint

State-changing requests are checked against the caller's role and written to the audit
log. The check is in the handler, so it applies equally to the panel, to `curl`, and to
anything else holding a session or a token.

State-changing requests that a browser sends from another origin are refused, including
from other subdomains of the panel's own domain, where your apps may run.

Errors are JSON with a stable code, `{"error":{"code":"...","message":"..."}}`. A path that
does not exist under `/api/v1` answers `404 not_found`, and a known path called with the
wrong method answers `405 method_not_allowed`, so a mistyped URL fails rather than
returning the panel's HTML with a 200.

Requests through a plugin's proxy follow the same roles: any signed-in user can read, and
anything other than `GET`, `HEAD` or `OPTIONS` needs `member`, as does opening a WebSocket.
Windlass does not pass the caller's cookie or `Authorization` header on to the plugin.

Rate limiting applies to authentication endpoints. API tokens are not rate limited: the
secret is 160 random bits, so guessing one is not a practical attack.

## Health

`GET /api/v1/system/health` needs no authentication and is the right target for an uptime
check.
