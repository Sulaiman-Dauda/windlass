# Life without the panel

Windlass' core promise: **if the panel disappears, nothing you deployed
breaks**, and everything it manages is operable with standard tools. This
page proves it.

## Where everything lives

```
/var/lib/windlass/
├── projects/<name>/     # one directory per project — THE source of truth
│   ├── compose.yaml     # plain Docker Compose, yours to edit
│   ├── .env             # rendered env vars (also editable by hand)
│   └── src/             # git checkout (git-sourced projects)
├── data/
│   ├── windlass.db        # SQLite metadata (users, deploy history, domains)
│   ├── secret.key       # encryption key for stored secrets (0600)
│   └── session.key      # session signing key
└── backups/             # tar.gz archives
```

## Operating a project by hand

```sh
cd /var/lib/windlass/projects/myapp
docker compose ps                 # status
docker compose logs -f web        # logs
docker compose up -d              # deploy after editing compose.yaml
docker compose down               # stop
```

The panel notices hand-edits: the next panel deploy simply runs the same
compose commands against the files as they are on disk.

## Routing without the panel

Windlass owns exactly one object in Caddy's config, tagged
`"@id": "windlass_routes"`. Inspect or remove it:

```sh
curl localhost:2019/id/windlass_routes          # view
curl -X DELETE localhost:2019/id/windlass_routes  # remove all panel routes
```

Everything else in your Caddy config is untouched by Windlass, always.
If you delete the panel, existing routes keep working until Caddy restarts;
make them permanent by adding normal Caddyfile sites.

## Secrets

`.env` files on disk contain the rendered values — that's what compose
actually uses, and it survives the panel. The encrypted copies in SQLite are
only the panel's editing store.

## Backups

Backups are ordinary `tar.gz` files in `/var/lib/windlass/backups/`:

```sh
tar xzf backups/myapp-20260713-101500.tar.gz -C projects/myapp
cd projects/myapp && docker compose up -d
```

Database templates include a `db_dump.sql` in the archive for native restore.

## The metadata database

`data/windlass.db` is SQLite — `sqlite3 data/windlass.db .schema` shows
everything. Losing it loses users/history/domain records but not a single
running container, compose file, or env var.
