# Contributing to Windlass

Thanks for looking. Windlass is a single Go binary that drives `docker compose`,
so most changes are smaller than they look — and a few are far more dangerous
than they look. This page is mostly about telling those apart.

## Before anything else

Windlass runs with Docker socket access on someone's server. A change that
widens what the web UI can reach is a security change, whatever else it does.
Read [SECURITY.md](./SECURITY.md), and report vulnerabilities privately rather
than in an issue.

## Getting set up

You need **Go 1.26+**, **Node 20+** and a working Docker with Compose v2.

```bash
git clone https://github.com/Sulaiman-Dauda/windlass.git
cd windlass
make dev            # builds the SPA and runs the binary against ./tmp
```

The web UI lives in `web/` and is embedded into the binary at build time, so a
frontend change needs a rebuild to appear in a release build.

## The rules that are not negotiable

These come from the architecture, not from taste. A PR that breaks one of them
will be turned down however good the code is:

1. **The project filesystem is authoritative.** `compose.yaml` owns service
   configuration, `.env` owns environment values, `.windlass.json` owns the small
   amount of Windlass-specific config. SQLite holds platform state and rebuildable
   indexes — never a database-only copy of Compose configuration.
2. **Deployed containers must survive Windlass being stopped or removed.** It is
   a control plane, not a runtime dependency.
3. **Privileged work stays in `internal/agent`.** Only `internal/agent/local`
   imports Docker/Moby packages or touches Docker, Caddy, project files, Git or
   exec directly. Everything else goes through the interface.
4. **Compose operations shell out to `docker compose`.** Do not reconstruct
   container specs — matching the real CLI is the point.
5. **Caddy changes are targeted `@id` operations.** Windlass owns
   `windlass_routes`, `windlass_panel_route` and their `windlass_route_*`
   children, and nothing else. Never load or replace a whole Caddy config.

## Pull requests

Fork, branch from `main`, and name it for what it does: `fix/compose-env-quoting`,
`feat/backup-retention`, `docs/caddy-model`.

- One concern per PR. A fix bundled with a refactor is two reviews pretending to
  be one.
- `make test` and `make lint` pass.
- Explain **why**, not just what. Describe the failure the change fixes.
- Say how you verified it. "Deployed a two-service stack, restarted Windlass,
  containers kept running" is what a reviewer needs.
- Update `docs/` if behaviour changed.

Small fixes are welcome without prior discussion. For anything structural — a new
agent operation, a change to the Caddy ownership model, a schema change — open an
issue first so the approach can be agreed before you spend a weekend on it.

## Code style

- `gofmt` and `go vet` clean.
- Comments explain **why**, not what. A comment restating the code is noise; one
  recording a constraint or a rejected alternative is the most valuable thing in
  the diff.
- Match the surrounding code. Consistency beats personal preference.

## What to expect from us

Windlass is maintained by one person alongside other work.

- **Issues** — usually looked at within a few days. A report with reproduction
  steps against a throwaway host gets attention fastest.
- **Pull requests** — a first response within about a week.
- **Security reports** — prioritised over everything else.

There is **no CLA and no DCO sign-off**.

## Licence

Contributions are accepted under the [Apache-2.0](./LICENSE), the same licence as
the project. By opening a pull request you confirm you have the right to
contribute the code under that licence.
