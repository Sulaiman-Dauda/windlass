# Windlass documentation

A Compose control plane that does not own your containers.

## Start here

- [Getting started](./getting-started.md), install, claim, deploy and route a first
  project on a real server
- [Install](./install.md), the installation methods in detail

## Using it

- [Projects and Compose](./projects-and-compose.md), how project directories, `.env`,
  limits and health labels work
- [Deployments](./deployments.md), the state machine, health gating, rollback and Git
  deployments
- [Domains and HTTPS](./domains-and-https.md), Caddy routes, certificates, and exactly
  what Windlass will and will not touch
- [Backups and restore](./backups-and-restore.md), archives, database dumps, S3 and what a
  restore does not do
- [Users and access](./users-and-access.md), roles, TOTP, OAuth sign-in and the privilege
  boundary

## Reference

- [Configuration](./configuration.md), every `WINDLASS_*` variable and its default
- [API](./api.md), the versioned HTTP API the panel itself uses
- [Architecture](./architecture.md), how it is put together and why it shells out to
  `docker compose`
- [Troubleshooting](./troubleshooting.md), the failures that are most often misread

## The part that matters

- [Life without the panel](./life-without-the-panel.md), what happens to your containers
  if Windlass stops, is removed, or was never there
