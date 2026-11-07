# 01 MCP read-only tools

Status: done 07/11/2026, committed on `feat/mcp`. Both checks for Pablo passed.
Phase 1. Depends on nothing (API tokens shipped in 0153eca).

## Goal

`/api/v1/mcp` answers MCP clients on both protocol revisions with seven read-only tools, so an
agent holding a `read` token can answer "what is running, is it healthy, what changed, what do
the logs say, is staging ahead of live". The first client is Claude Code, on a local scratch
instance.

## Fills

- `internal/mcp`: `New`, `SetAPI`, `ServeHTTP`, `Tools`.
- Private code in `internal/mcp` as needed: JSON-RPC envelope, version negotiation, header
  checks, the seven tools, log scrubbing.
- Wiring, not stubs: an `MCP http.Handler` field on `api.API`, mounted at `/mcp` inside the
  authenticated group when non-nil; `cmd/windlass` builds the server, sets `Config`
  (`EnvValues` from the projects service, `Audit` from the audit log) and calls `SetAPI` with
  the handler `server.New` returns.

## Decisions

See `ARCHITECTURE.md`. Settled by Pablo 30/10/2026, do not reopen:

- Hand-written protocol, no MCP SDK, no new dependencies.
- Stateless, JSON responses only. 2026-07-28: `server/discover`, required `MCP-Protocol-Version`,
  `Mcp-Method`, `Mcp-Name` headers checked against the body. 2025-11-25: `initialize` answered
  without `Mcp-Session-Id`; `notifications/initialized` gets 202.
- Tools call `/api/v1` in process by building a request with the caller's `Authorization`
  header and `RemoteAddr`, sent to the root handler. Never call services directly.
- Log scrubbing: replace the project's env values (non-empty, at least 6 characters, so `1` or
  `true` are not blanked everywhere) and anything matching `wl_pat_[0-9a-f]{60}` with
  `[redacted]`.
  Amended 07/11/2026 after the code review (flagged for Pablo): the values also include those
  the top-level Compose files set inline, the ones the panel masks from viewers
  (`api.MCPEnvValues`, which reuses the masker's walk), with the value on each `key = value`
  line of config content; a Compose file that will not parse withholds the text. Under a key that names a secret (`PASS`, `PWD`, `SECRET`, `TOKEN`,
  `PRIVATE`, `CREDENTIAL`, `API_KEY`, `*_KEY`) the floor is 4 characters, booleans excepted.
- Tools: `list_projects`, `get_project` (services and their state, domains, last deployment),
  `list_deployments`, `get_deployment` (with its events), `get_logs` (project, optional
  service, tail up to 500 lines, no follow), `get_staging`, `list_backups`. All
  `readOnlyHint: true`, `destructiveHint: false`, `idempotentHint: true`, `openWorldHint: false`.
- `get_logs` reads the existing logs route with `follow=false`.

## Touches

`internal/mcp/`
`internal/api/api.go`
`cmd/windlass/main.go`
`docs/build/01-mcp-read-tools.md`
Also, flagged: `internal/projects/service.go` (`ReadEnv` made a pure read), and from the
review `internal/api/composemask.go` (a recording hook) with its test.

## Not in this unit

- Write tools, `deploy` scope, migration 0010: unit 3.
- The agent eval set: unit 2.
- `docs/mcp.md`, the site allowlist, CI wiring of the conformance suite, release: unit 4.
- OAuth, `WWW-Authenticate`, resource metadata: unit 5.
- Anything on the live host.

## Done when

- [x] `go build ./... && go vet ./... && scaffold-check` passes, `internal/mcp` shows 0 stubs left
- [x] `go test ./internal/mcp/...` passes, with tests named after the README rules:
      `TestRefusesCookieOnly`, `TestOriginIs403`, `TestGetAndDeleteAre405`,
      `TestLatestNeedsHeaders`, `TestLegacyInitializeHasNoSession`, `TestNoToolReturnsEnv`,
      `TestLogsScrubbed`, `TestLimitsAreReported`, `TestEveryToolHasAllAnnotations`
- [x] A server test through the real router: a `read` token can call all seven tools;
      a disabled user's token gets 401; a revoked
      token gets 401
- [x] `make test` passes
- [x] Idle RSS of the built binary under 40 MiB, measured the way CI does
      (23,536 kB on 07/11/2026)
- [x] `npx @modelcontextprotocol/conformance@0.1.16 server --url http://127.0.0.1:<port>/api/v1/mcp`
      on the scenarios that apply to tools-only servers, output in the chat. Whether it can send
      a bearer header is unchecked; if it cannot, put a throwaway local proxy in front that
      adds the header. No test-only auth path in the binary.
      Now a Go test, `go test -tags mcpconformance ./internal/server -run TestMCPConformance`,
      and a CI job (unit 4); it expects only `localhost-host-valid-accepted` to fail, as any
      `Origin` gets 403. Real suite run by Pablo 07/11/2026: initialize, ping, tools-list and
      rebinding-rejected pass, the by-design check fails, test PASS
- [x] Local scratch instance: `claude mcp add --transport http` with a `read` token, then in
      Claude Code list projects, read a project's logs, and see a planted env value come back as
      `[redacted]`.
      Partly done: the same exchange replayed with curl on 07/11/2026 against a Podman
      scratch project (legacy initialize on 2025-06-18 and 2025-11-25, tools/list, all seven
      tools, one 2026-07-28 call); the echoed password came back as `[redacted]`. Claude
      Code session run by Pablo 07/11/2026 (`.mcp.json`, token from the environment): 4
      calls listed 8 projects, diagnosed eval-down and showed its logs with the password
      as `[redacted]`
