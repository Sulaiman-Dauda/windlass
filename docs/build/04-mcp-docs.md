# 04 MCP docs and release

Status: docs built 07/11/2026, committed on `feat/mcp`. Not tagged: it ships in v0.60, which Pablo tags.
Phase 4. Depends on 01 and 03.

## Goal

An operator can connect Claude Code, Cursor, VS Code or Codex to their panel from the docs
alone, knows what an agent can and cannot see or do, and the page publishes on windlass.run
when this branch merges.

## Fills

- `docs/mcp.md`: connect steps, a snippet for each of the four clients, a curl check, the
  twelve tools, what an agent never sees, writes and `confirm`, limits, protocol revisions,
  troubleshooting.
- Links to it from `docs/api.md` (after the five deploy routes) and `docs/README.md`
  (Reference).
- `README.md` feature list: the token line now names all four kinds, and one line for MCP.
  The lead and the comparison are untouched.
- `windlass-site/scripts/sync-docs.mjs`: `mcp.md` in PAGES, Reference group, after `api.md`.
  That is a separate repository, also uncommitted.
- CI wiring of the conformance suite, which unit 1 left here: `TestMCPConformance` in
  `internal/server/mcp_conformance_test.go` (build tag `mcpconformance`) and a
  `MCP conformance suite` job in `.github/workflows/ci.yaml`.

## Decisions

Settled during the build 07/11/2026, flagged for Pablo:

- **Where the snippets come from.** Claude Code: `claude mcp add --help` on this laptop and
  its docs for `.mcp.json` with `${VAR}`. Codex: the installed CLI wrote the exact TOML in the
  page (`codex mcp add --url ... --bearer-token-env-var`, in a temporary `CODEX_HOME`).
  Cursor (`${env:VAR}`) and VS Code (`inputs`, `${input:id}`) from their docs; neither was
  run against the panel.
- **The site entry can land before the merge.** The sync skips a listed page missing from
  `/docs`, in the content and the sidebar alike, and CI clones `main`. The page appears on
  windlass.run on the first site build after `feat/mcp` merges.
- **Conformance as a Go test, not a proxy script.** The suite cannot send headers, so the
  test serves the router with the fake agent behind a listener that adds a read token. Four
  scenarios apply (initialize, ping, tools-list, DNS rebinding); 0.1.16's others call the
  suite's own fixture tools. Results are judged per check from `checks.json`, as the CLI
  exits 1 on the by-design failure, and that check passing fails the test so the list stays
  true. npx fetches the suite's dependencies unpinned in CI, with no secrets in the job.
- **README: two lines only.** The token line said "read-only or read-write", which unit 3
  made wrong. Anything more is launch copy, Pablo's.

## Touches

`docs/mcp.md`, `docs/api.md`, `docs/README.md`, `README.md`,
`docs/build/04-mcp-docs.md`, `windlass-site/scripts/sync-docs.mjs`,
`internal/server/mcp_conformance_test.go`, `.github/workflows/ci.yaml`

## Done when

- [x] `docs/mcp.md` passes the prose check
- [x] The curl check in the page answers on scratch with a real token
- [x] `node scripts/sync-docs.mjs` in the site renders the page: 14 pages, links to
  `/guides/api/` and `/guides/api/#behind-cloudflare`, the sidebar entry under Reference
- [x] The conformance test judges a stand-in suite right: a clean run passes; the by-design
  check passing, a FAILURE, or no `checks.json` each fail it
- [ ] The conformance job green against the real suite (Pablo, or CI on the first push)
- [ ] Cursor and VS Code snippets tried in the clients (Pablo)
- [ ] v0.60 tagged by hand, after the units' open boxes (Pablo)
