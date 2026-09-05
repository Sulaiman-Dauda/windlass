# Backups and restore

A backup is a compressed archive of the project directory: `compose.yaml`, `.env`, and
whatever else lives alongside them. For projects that look like one of the database
templates, Windlass also runs a native dump into the project directory first, so the
archive contains it.

## Taking one

**Back up now** in the project's Backups tab. Backups are listed with their status, and an
incomplete one cannot be restored.

The database dump is best effort and deliberately non-fatal. If the database container is
not running, the dump is skipped with a warning and the file archive is still taken. A
backup that captured your compose file and environment is worth having even on a day the
database was down.

Postgres projects are dumped with `pg_dump`, MySQL and MariaDB with `mysqldump
--all-databases`, both executed inside the running container.

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
file, and the other files that were there, the database dump among them.

**It does not load the dump back into your database.** Restoring gives you the dump file
in the project directory; putting its contents back is a deliberate act you perform, with
the database in the state you intend:

```sh
cd /var/lib/windlass/projects/shop-api
docker compose up -d db
docker compose exec -T db psql -U postgres < dump.sql
```

This is the conservative reading of what a restore button should do. Silently replaying a
dump over a live database is the kind of convenience that eventually destroys someone's
production data, so Windlass restores the files and leaves the decision with you.

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
