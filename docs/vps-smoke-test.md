# VPS smoke test

The manual checklist for validating a release on a real server. Takes ~15
minutes on a fresh Ubuntu 24.04 VPS with a DNS name pointed at it.

## 1. Install

```sh
curl -fsSL https://raw.githubusercontent.com/windlass-dev/windlass/main/install/install.sh | sudo sh
```

Expect: Docker/Caddy/git detected or installed, `windlass` systemd unit
active, login URL printed.

- [ ] `systemctl status windlass` → active (running)
- [ ] `journalctl -u windlass | grep setup_token` shows a token
- [ ] `curl localhost:8080/api/v1/system/health` → `{"status":"ok",...}`

## 2. Claim the instance

Open `http://<ip>:8080`, paste the setup token, create the admin account.

- [ ] Dashboard shows host CPU/memory/disk and docker/compose/caddy versions

## 3. Deploy something

Projects → New project → deploy (starter nginx compose).

- [ ] Deployment reaches **succeeded** with live logs streaming
- [ ] Overview tab shows the service running
- [ ] `docker ps` on the server shows the container

## 4. Domain + HTTPS

Domains tab → add `app.<your-domain>` → service `web`, port `80`.

- [ ] Status becomes **active** within ~10s
- [ ] `https://app.<your-domain>` serves the app with a valid certificate

## 5. Database template

Templates → PostgreSQL → create.

- [ ] Deployment succeeds; Environment tab shows generated credentials
- [ ] `psql "postgres://windlass:<password>@127.0.0.1:5432/<name>"` connects

## 6. Git deploy

Point a project at a repo (Git tab), add the webhook to GitHub, push a commit.

- [ ] Push triggers a deployment (webhook audit entry, deployment marked `webhook`)
- [ ] Rollback to the previous deployment succeeds and pins digests

## 7. Backup / restore

Backups tab → Back up now → edit compose.yaml → Restore.

- [ ] Restored file matches the backup

## 8. The panel is removable (principle 7)

```sh
sudo systemctl stop windlass
```

- [ ] Deployed app still serves through Caddy
- [ ] `docker compose -p <project> ps` works — it's a plain compose project

```sh
sudo systemctl start windlass
```

- [ ] Panel comes back; interrupted deployments (if any) resume

## 9. Self-update (after a second release exists)

Settings → check for updates → apply.

- [ ] Panel restarts into the new version; `windlass.previous` exists next to
      the binary; app containers untouched

## 10. Resource budget

```sh
systemctl show windlass -p MemoryCurrent
```

- [ ] Idle RSS < 40 MB after 10 minutes of uptime
