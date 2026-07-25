# Windlass — agent brief

Self-hosted Docker Compose control plane: projects, Git deployments, domains with HTTPS, logs,
terminals, metrics, backups, templates, users, updates. Go single binary, `go 1.26`,
module `github.com/windlass-dev/windlass`. See `README.md` for the product model and
`CLAUDE.md` for architecture rules — read both before changing code.

## Status: IN PRODUCTION

**Windlass is in real use — it is not a prototype.** Abio deploys other projects with it,
including Stockroom. A regression here doesn't break a demo, it breaks the thing that runs
other live services. Treat every change to deployment, proxy, domain or backup logic as
high-consequence, and prefer additive changes over rewrites.

## Run it

```sh
make dev            # local development
make build          # single binary into bin/
make build-web      # frontend assets
make build-cross    # cross-compiled release binaries
make lint
```

Layout: `cmd/` entrypoints, `internal/` the bulk of the logic, `api/` HTTP surface,
`migrations/` schema, `plugins-sdk/` the plugin contract, `install/` the installer.

## Tests

```sh
make test              # unit
make test-integration  # integration — slower, needs Docker
```

Run `make test` before handing anything back. Run `make test-integration` for changes to
deployment, proxy routing, domains or backups.

## Hard rules

1. **Never point a local run at a production Windlass instance or its database.** Develop
   against a local instance with throwaway projects.
2. **Never test destructive panel operations against real projects** — project deletion, domain
   removal, backup restore and update paths get exercised on scratch projects only.
3. **Stage and show the diff; get an explicit go before committing or pushing.**
4. Migrations in `migrations/` are forward-only against real installs. A migration that has
   shipped is never edited in place — add a new one.
5. Don't break the plugin SDK contract without saying so plainly; third-party plugins depend on it.

## Gotchas

- The source-of-truth model is documented in `README.md` and is the thing most likely to be got
  wrong — Windlass does not replace Docker Compose, it drives it. Changes that make the panel the
  authority rather than the compose file are architecturally wrong.
- `bin/` holds build output and is disposable; never hand-edit it.
