# Staging

A staging is a second copy of a project, with its own domains, environment and data, that
changes reach first. Pushes deploy to staging. Live changes only when you promote a staging
deployment that succeeded, and promotion deploys exactly what staging ran: the same Git
commit and the same image digests.

A staging is an ordinary Compose project on the same server, named after live with
`-staging` on the end (`shop` gets `shop-staging`). Its directory, compose files and `.env`
are its own, and `docker compose` treats it like any other project.

## Creating one

Open the live project's overview and choose **Create staging**. Nothing changes until you
confirm, and the dialog shows everything it is about to do:

- **Branch.** For a Git project, the branch staging deploys from. It defaults to live's.
- **Domains.** Each of live's domains gets a staging domain, `staging-app.example.com` for
  `app.example.com` and `staging.example.com` for an apex. Every live hostname in the copied
  compose files and `.env` is replaced by its staging hostname, exact matches only, and the
  dialog lists each replacement.
- **Values that may reach real people or services.** Environment keys that look like mail,
  SMS, payments, webhooks, storage or automation (`SMTP_HOST`, `STRIPE_SECRET_KEY`,
  `ZAPIER_HOOK` and similar) are listed with live's value. Give staging its own test keys
  here. A field left empty keeps live's value, and you confirm you have checked the list
  before staging can be created. A key like these that a service gets from an `env_file` or
  the compose file itself, rather than `.env`, is listed as a warning: staging copies those
  files as they are, so it keeps live's value unless you change staging's copy.
- **Copy live's data.** On by default when live has volumes. Named volumes are copied file
  by file. A Postgres, MySQL or MariaDB volume is not copied as files, because a running
  database's files are not safe to copy: Windlass dumps live's database and loads the dump
  into staging's own fresh server. Live keeps running throughout. Only one database is
  copied: with a second, its volume is left out and staging's starts empty, and the dialog
  says so. The dialog shows the size and the free disk, and refuses a copy that would leave
  less than 1 GiB free.
- **Promote automatically.** Off by default. See [Promoting](#promoting).

Staging then deploys for the first time and its log opens.

### Recovering an interrupted first data copy

If Windlass stops during the first data copy, that deployment fails with
"data copy was interrupted; delete staging and create it again". Loading it a second
time into the partial database or volume would be unsafe.

On that staging project's overview, a member can choose **Recreate staging**. Confirm the
project name, re-enter your account password if you have one, and confirm reuse of the
current environment values. Windlass reads staging's branch, domains, `.env`, hostname
rewrites and auto-promote setting before deleting it, then creates it with those settings
and copies live's data from the beginning. Live keeps running. Staging's other file edits
are replaced with live's current files. A manually installed push webhook may need its new
secret; the dialog shows it once if Windlass cannot register the webhook automatically.

The existing deletion and creation checks still apply. If removing staging's containers
and volumes fails, its directory and history are kept so you can try again. Once deletion
succeeds it cannot be undone: if the new creation preflight finds a blocker, staging stays
deleted. This action is only available for that interrupted first copy, not for another
failed deployment or a staging that has already been used.

If Windlass cannot add the push webhook to the repository itself, the dialog shows the
payload URL and secret once so you can add it by hand. Projects connected through the
Windlass GitHub App need no webhook.

### What stops a staging being created

A copy on the same server must not share anything with live that it could write to. These
block creation, each with the fix shown in the dialog:

- `container_name`, a published host port, `network_mode: host` or another container's
  network
- an `external` network, where live's services answer to the same names
- an `external` volume, or a volume with a fixed `name:`
- a writable bind mount outside the project directory (read-only mounts such as
  `/etc/localtime:ro` are allowed and listed)
- a `windlass.health.url` that still checks live's domain
- a service that builds on the server when live is not connected to Git, because promotion
  would have nothing to deploy but live's own files

The same isolation check runs at the start of every staging deployment, so a compose file
edited later cannot start sharing with live without the deployment failing.

A service that builds into a fixed `image:` tag is allowed but warned about: live and
staging would build into the same tag. Use `image: ${COMPOSE_PROJECT_NAME}-web`, or drop
`image:` so Compose names it per project.

## Promoting

The pair card on either project's overview shows what live runs, what staging last
verified, and **Promote #N**. Promote creates a new live deployment that:

- checks out staging's commit, for a Git project, and builds from it
- pins every pulled image to the digest staging ran, and skips pull
- uses live's own compose files, `.env` and data, which promotion never touches

A promoted deployment is marked "promoted from staging #N" in live's history and is health
checked like any other, so a promotion that fails verification fails on live the same way a
deployment would. Roll it back from the Deployments tab as usual.

Promote is unavailable while either project is deploying, before staging has a deployment
that succeeded, and when live already runs that staging deployment or its commit.

**Promote automatically** promotes each staging deployment that succeeds. Staging's first
deployment, the one that creates it, is never promoted, and neither is a rollback on staging:
promoting it would roll live back too. Roll live back from its own Deployments tab.

**Deploy** on a live project that has a staging still works, behind a confirmation that it
skips staging. Pushes to live's branch are ignored while the staging exists: they deploy to
staging instead.

## Compose files that differ

Live and staging keep separate compose files, so a change made on staging (a new service, a
changed label) does not reach live by promoting. The pair card lists every top-level compose
file that differs once the staging hostnames are put back, with **View diff**. **Copy to
live** writes staging's version into live's directory with live's hostnames, and live uses
it from its next deployment or promotion.

`.env` is never compared or copied: environment values are meant to differ.

## What is and is not guaranteed

Guaranteed:

- staging's containers, networks and volumes are its own Compose project, and the isolation
  check above runs before every staging deployment
- staging's domains send `X-Robots-Tag: noindex`, so search engines do not index them
- staging's `.env` sets `WINDLASS_ENVIRONMENT=staging`
- promotion deploys staging's commit and image digests, and nothing from staging's files

Not guaranteed:

- **Mail, payments and webhooks.** Windlass cannot see what an application sends. A key the
  dialog did not flag, a value in a compose file rather than `.env`, or a key left at live's
  value will reach the real service. Guard the application itself:

  ```yaml
  environment:
    APP_ENV: ${WINDLASS_ENVIRONMENT:-production}
  ```

  `APP_ENV` is `staging` on staging and `production` everywhere else, so the application can
  send nothing outside the server unless it is `production`.
- **URLs the rewrite misses.** Only exact hostnames in compose files and `.env` are replaced.
  A URL built from parts, one in an `env_file`, or a hostname inside a database row, still
  points at live.
- **Copied data is a snapshot.** Staging does not follow live afterwards. To refresh it,
  delete staging and create it again.
- **Shared outside services.** A database, bucket or queue outside the project, reached
  through an address in `.env`, is the same one live uses unless you changed it.

## Deleting

Delete staging from its own page. Its containers, its directory and its volumes are removed
(`docker compose down -v`, which removes only the project's own volumes, never external
ones), and live goes back to deploying on push if it did before. A live project with a
staging cannot be deleted until its staging is.

Delete is refused while staging's compose file gives a volume a fixed `name:`. Staging
deployments already refuse one, but a file edited since would make `down -v` remove that
volume, even if it is live's. Remove `name:` and delete again.

## Where the link lives

Staging's `.windlass.json` names its live project, its hostname rewrites and whether it
promotes automatically:

```json
{ "staging": { "of": "shop", "auto_promote": false } }
```

The database keeps an index of the link that a fresh database rebuilds from that file, so
the pair survives a lost or replaced SQLite file like everything else.
