# Windlass MCP: architecture

This file covers only the MCP endpoint, the one part of Windlass under `scaffold-check`
(`scaffold.json`). The product as a whole is described in `docs/architecture.md` and
`CLAUDE.md`.

The MCP endpoint lets an AI agent (Claude Code, Cursor, VS Code, Codex CLI) see and operate a
Windlass box with a personal API token. The aim is the safest panel to hand an agent: Compose
stays the authority, staging comes before live, the tool list is short, and env values never
reach the model.

## Never

- Return env values, `.env` contents or files under a project's env path, whatever the token.
- Offer a tool for a route that refuses tokens: terminal, token and user management, update,
  panel domain, project delete, backup restore, staging recreate.
- Accept a session cookie alone, answer a browser `Origin`, or send CORS headers.
- Write Compose files. Agents change code in Git and deploy it.
- Hold protocol sessions or any state between requests.

## Out of scope for the first release (v0.60)

- OAuth one-click for Claude.ai and ChatGPT (unit 5, only if units 1 to 4 show real use).
- SSE responses, progress notifications, elicitation. Destructive tools use annotations plus a
  required `confirm` argument instead.
- Resources and prompts. Tools only.

## Nouns

| Noun | What it is | Owned by | Key fields |
|---|---|---|---|
| Tool | One catalogue entry an agent can call | `internal/mcp` | name, title, schemas, annotations |
| Token | A personal API token, the only credential | `internal/auth` | scope, secrets flag, projects |

## Modules

| Module | Owns | Does not own | May use |
|---|---|---|---|
| `internal/mcp` | protocol, catalogue, output limits, log scrubbing | permissions, masking, audit of REST routes, tokens | nothing |

`internal/api` mounts the server inside its authenticated group; `cmd/windlass` builds it and
hands it the root handler. Tools call `/api/v1` in process with the caller's own
`Authorization` header, so permissions are the REST API's and cannot drift.

## Permissions

A token acts as its user, capped at its scope (`read`, `write`, `admin`; `deploy` arrives in
unit 3 with migration 0010). MCP adds no permission of its own and can only narrow.

## Failure and rollback

Worst case: an agent deploys or promotes something wrong. Every write goes through the same
audited route as the UI, with the token named, and rollback is the existing deployment
rollback. The endpoint itself is removed by unmounting one route; it holds no data.

## Deployment

Same binary, no new dependencies, idle RSS under the 40 MiB CI budget. Built and tested on a
local scratch instance; the live host only after a unit passes there and Pablo says go.

## Additions in v0.63 (agreed 21/11/2026)

Two tools and one field, each shipped in its feature's PR with its `docs/mcp.md` row and the
`Tools()` comment updated. No locked line changes, so no relock.

- `check_domains` (U9 domain checks): read-only. It calls a GET route that read tokens can use,
  and sets `OpenWorldHint` true, because it does DNS lookups and TLS dials to outside hosts.
- `cancel_deployment` (U11 deploy queue): deploy scope, required `confirm`. It takes a project
  and cancels only that project's waiting deploy, never one already running. Its route sits in
  the `RequireDeployer` group. A deploy held in the in-memory queue has the state `waiting` and
  no deployment number: `deploy` answers `waiting` with the queue position, and `get_project`
  shows the queue. `rollback` and `promote` keep their 409.
- `get_staging` gains `password_protected` (U12 staging password). The bcrypt hash is never
  returned: the project file route masks it in `.windlass.json` for anyone who cannot read
  secrets, with a test.
- The audit page (U10) and notification channels (U13) get no tool. Audit is admin-only, and
  channel settings hold webhook secrets and SMTP passwords.

## Open questions
