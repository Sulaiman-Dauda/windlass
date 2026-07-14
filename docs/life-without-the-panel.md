# Life without the panel

Windlass is deliberately removable from the application runtime path. Docker keeps containers
running and Caddy keeps its active configuration when the Windlass process stops.

## Files on disk

```text
/var/lib/windlass/
├── projects/<name>/
│   ├── compose.yaml       # authoritative Compose configuration
│   ├── .env               # authoritative dotenv values, mode 0600
│   ├── .windlass.json     # Git/source/domain metadata used for index rebuilds
│   └── ...                # source and application files
├── data/
│   ├── windlass.db        # platform state and rebuildable indexes/caches
│   ├── secret.key         # encryption key, mode 0600
│   └── session.key        # session-signing key, mode 0600
├── backups/               # local project archives
└── plugins/               # optional plugin directories
```

## Operate a stack directly

```sh
cd /var/lib/windlass/projects/myapp
sudo docker compose -p myapp config
sudo docker compose -p myapp ps
sudo docker compose -p myapp logs -f web
sudo docker compose -p myapp up -d --remove-orphans
sudo docker compose -p myapp down
```

These are the same files and Compose project name Windlass uses. A hand edit is visible in the
Files/Environment screens on their next read, and the next deployment uses it directly. A
manual `docker compose up -d` updates containers immediately; Windlass reads live container
state rather than overwriting the file from SQLite on the next deployment.

## Rebuild the application index

If `windlass.db` is replaced with a new database, create/claim a new administrator and choose
**Scan stacks directory**. Windlass discovers directories containing `compose.yaml` or
`compose.yml`, reads `.windlass.json`, imports `.env`, and rebuilds project/domain indexes.
Running containers are unaffected throughout.

The following are platform state and cannot be reconstructed from project files: users,
sessions, audit history, deployment/event history, jobs, backup records/schedules, settings,
plugin enablement, and encrypted Git/S3/OAuth credentials. Back up the complete `data`
directory when those records matter.

## Caddy without Windlass

Windlass-owned objects can be inspected through the loopback Caddy admin API:

```sh
curl http://127.0.0.1:2019/id/windlass_routes
curl http://127.0.0.1:2019/id/windlass_panel_route
```

Application routes and the panel route remain in Caddy's active in-memory configuration when
Windlass stops. A Caddy reload/restart reconstructs configuration from the administrator's
persistent Caddy config, then Windlass normally reapplies its desired routes. If Windlass has
been permanently removed, add any routes that must survive Caddy restarts to the Caddyfile.

To remove only Windlass-owned routes manually:

```sh
curl -X DELETE http://127.0.0.1:2019/id/windlass_routes
curl -X DELETE http://127.0.0.1:2019/id/windlass_panel_route
```

Unrelated Caddy objects are not touched.

## Secrets

Compose requires `.env` values in plaintext on disk, protected by filesystem ownership and
mode `0600`. The encrypted SQLite environment copy is a cache, not the source of truth. Git,
OAuth, TOTP, and S3 credentials remain encrypted platform state and need `secret.key` for
recovery.

## Project backups

Local backups are ordinary archives under `/var/lib/windlass/backups`. Restore through the UI,
or inspect/extract one with standard archive tools. Windlass attempts a native SQL dump for
recognized PostgreSQL/MySQL template stacks before archiving; failure of that best-effort dump
does not prevent the filesystem archive.
