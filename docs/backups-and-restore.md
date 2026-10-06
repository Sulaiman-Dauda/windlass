# Backups and restore

A backup is a compressed archive of the project directory: `compose.yaml`, `.env`, and
whatever else lives alongside them. When the project has a Postgres, MySQL or MariaDB
container, Windlass also takes a native dump and adds it to the archive as
`.windlass/backup/db_dump.sql`.

## Taking one

**Back up now** in the project's Backups tab. Backups are listed with their status, and an
incomplete one cannot be restored.

The database container is recognised by its compose service name or its image, preferring
one named exactly after the database (`postgres`, `postgis`, `pgvector`, `timescaledb`,
`mysql`, `mariadb`, `percona-server`) over an image that only mentions it, such as
`bitnami/postgresql-repmgr`. The dump runs inside that container with its own environment,
so the credentials are found wherever you set them: in `compose.yaml`, in `.env` or in
another `env_file`.

Postgres is dumped with `pg_dump` as `POSTGRES_USER` (default `postgres`) from
`POSTGRES_DB`, using `POSTGRES_PASSWORD` when it is set. Bitnami's `POSTGRESQL_USERNAME`,
`POSTGRESQL_DATABASE` and `POSTGRESQL_PASSWORD` are read too. MySQL and MariaDB are dumped with `mysqldump` or `mariadb-dump
--all-databases` as root, using `MYSQL_ROOT_PASSWORD` or `MARIADB_ROOT_PASSWORD`. A
database whose credentials are not in its environment, such as a random root password or
one read from a secrets file, cannot be dumped this way.

The dump is best effort and deliberately non-fatal. If the database container is not
running or the dump fails, Windlass logs a warning saying the backup has no database dump
and still takes the file archive. A backup that captured your compose file and environment
is worth having even on a day the database was down.

## Scheduling

Set an interval of hourly, daily or weekly, a destination, and how many to retain. Older
backups beyond the retention count are removed as new ones succeed.

Pick a retention that matches what the archives cost you: they contain your `.env`, so
they are secrets at rest wherever they land.

## Off-server storage with S3

Configure an S3-compatible endpoint under Settings and choose S3 as a backup destination.
Anything speaking the S3 API works, including MinIO, which is what the wire tests run
against.

Backups on a server are a hedge against a mistake. Backups off the server are a hedge
against losing the server. If the data matters, use the second kind.

## Restoring

Restore replays the **project directory** from the archive: compose file, environment
file, and the other files that were there, the database dump among them at
`.windlass/backup/db_dump.sql`.

**It does not load the dump back into your database.** Restoring gives you the dump file
in the project directory; putting its contents back is a deliberate act you perform, with
the database in the state you intend:

```sh
cd /var/lib/windlass/projects/shop-api
docker compose up -d db
docker compose exec -T db psql -U postgres < .windlass/backup/db_dump.sql
```

This is deliberate. A restore that replayed a dump automatically would overwrite whatever
the database currently holds, with no way to inspect it first, so Windlass restores the
files and leaves that step to you.

## What is not in a project backup

Platform state lives in SQLite: users, sessions, audit history, deployment history,
settings and encrypted credentials. Project backups do not contain it.

Restoring a project directory onto a fresh install gives you a working application, and
**Scan stacks directory** indexes it. Your accounts and history need a platform backup,
which is a copy of the data directory taken while the service is stopped:

```sh
sudo systemctl stop windlass
sudo tar czf windlass-platform-$(date +%F).tar.gz -C /var/lib windlass
sudo systemctl start windlass
```

Keep that archive somewhere other than the machine it came from.
