# internal/mcp

The Model Context Protocol endpoint at `/api/v1/mcp`: lets an AI agent see and run a Windlass
box with a personal API token, safely. Plain Compose underneath, staging before live, a few
tools, and env values never reach the model.

- **Owns:** the MCP protocol (revisions 2026-07-28 and 2025-11-25, stateless, JSON only),
  the tool catalogue, output limits, and scrubbing secrets out of log text.
- **Does not own:** permissions, masking, auditing of REST routes (the routes it calls do
  that, in `internal/api` and `internal/auth`), tokens (`internal/auth`), any project,
  deployment or staging logic.
- **May use:** nothing in Windlass. It reaches the API as an `http.Handler` and gets env values
  and the audit hook through `Config`, filled in by `cmd/windlass`.
- **Rules it enforces:**
  - A bearer token is required; a session cookie alone is refused.
  - Any request with an `Origin` header gets 403. GET and DELETE get 405.
  - No tool returns env values or env files, whatever the token's flags.
  - No tool exists for a route that refuses tokens (terminal, delete, restore and the rest).
  - Lists stop at `MaxListItems`; logs at `MaxLogLines` and `MaxLogBytes`, tail only.
  - Log text has the project's env values and any `wl_pat_` token replaced before it is
    returned.
  - Every tool sets all four annotations. Destructive tools (unit 3) also need `confirm: true`.
- **Other modules call:** `New`, `SetAPI`, `ServeHTTP` (wiring only), `Tools`.
