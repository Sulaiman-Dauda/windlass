# VPS release smoke test

Run this checklist on Linux with Docker, Compose, Caddy, and two DNS names pointed at the
server: one for Windlass and one for a test application.

## 1. Install and claim

```sh
curl -fsSL https://raw.githubusercontent.com/windlass-dev/windlass/main/install/install.sh | sudo sh
sudo systemctl is-active docker caddy windlass-docker-proxy windlass
sudo journalctl -u windlass | grep setup_token
curl -fsS http://127.0.0.1:8080/api/v1/system/health
```

- [ ] All four services are active.
- [ ] The health response contains `"status":"ok"`.
- [ ] `curl http://127.0.0.1:2375/auth` returns 403.
- [ ] `sudo -u windlass docker -H unix:///var/run/docker.sock info` is denied.
- [ ] Claim the instance at the temporary port-8080 URL or through an SSH tunnel.

## 2. Configure the proper panel hostname

Open **Settings → Panel domain**, enter the DNS hostname, and save.

- [ ] `https://<panel-hostname>` loads with a valid certificate.
- [ ] `curl http://127.0.0.1:2019/id/windlass_panel_route` shows only that hostname and the
      configured panel upstream.
- [ ] An administrator-owned Caddy route still works.
- [ ] Re-saving and removing/re-adding the hostname does not duplicate or delete other routes.

After this succeeds, close public port 8080 or bind Windlass to loopback.

## 3. Create, bulk-configure, and deploy a project

Create a project, open Environment, paste multiple `KEY=value` lines, import them, and save.
Deploy the starter stack.

- [ ] `.env` on the server contains the values and is mode `0600`.
- [ ] Deployment events reach `succeeded`.
- [ ] `docker compose -p <project> ps` reports a running service.
- [ ] Overview displays Compose CPU/memory limits, or clearly says no limit is configured.

## 4. Hand-edit and reconcile

Edit `compose.yaml` and `.env` over SSH, then run:

```sh
cd /var/lib/windlass/projects/<project>
sudo docker compose -p <project> up -d
```

- [ ] The Files and Environment screens show the hand edits after refresh.
- [ ] The Overview shows the live container state.
- [ ] A later UI deployment keeps the hand-edited configuration.
- [ ] Comments/key order remain unchanged when the raw file editor saves unchanged text.

Copy a new directory containing `compose.yaml` and `.windlass.json` into the projects root,
then use **Scan stacks directory**.

- [ ] The imported project and its domains appear without manually inserting database rows.

## 5. Application domain and health verification

Add the application DNS hostname in the Domains tab with a real Compose service and declared
container port.

- [ ] Invalid service names and undeclared ports are rejected.
- [ ] The valid route becomes active and serves trusted HTTPS.
- [ ] Add `windlass.health.*` labels and confirm a bad response fails deployment verification.
- [ ] Correct the endpoint and confirm the stability window passes.

## 6. Git, rollback, backup, and restore

- [ ] Configure a Git connection and project; a signed webhook triggers a deployment.
- [ ] Roll back and confirm the recorded image digest is used.
- [ ] Create a local backup, modify a project file, restore, and verify the file is restored.
- [ ] If testing S3, confirm the API/UI never returns stored secret values.

## 7. Database-loss behavior

Back up the data directory first. Stop Windlass, move `windlass.db` aside, start Windlass, claim
the fresh instance, and scan the stacks directory.

- [ ] Existing application containers and HTTPS routes keep serving during database removal.
- [ ] Projects, manifests, domains, and `.env` values are rediscovered.
- [ ] The old users, audit/deployment history, settings, and encrypted credentials are absent,
      as documented.
- [ ] Restore the original database and keys after the test.

## 8. Panel removal and Caddy restart distinction

```sh
sudo systemctl stop windlass
```

- [ ] Applications and current Caddy routes still serve.
- [ ] Direct `sudo docker compose` operation works.

Restart Caddy while Windlass remains stopped.

- [ ] Administrator-owned Caddyfile routes return.
- [ ] Windlass runtime-only routes do not return until Windlass starts and reconciles them.

## 9. Image cleanup and resource budget

- [ ] Settings shows image total/reclaimable storage.
- [ ] Cleanup preserves running images and recent successful-deployment digests.
- [ ] The Go process remains below the CI 40 MiB idle-RSS budget.
- [ ] Record Caddy and socket-proxy memory separately; do not report them as Go-process RSS.

## 10. Automated gates

```sh
go test ./...
go vet ./...
cd web && npm ci && npm run build
go test -tags integration -count=1 ./...
```

- [ ] Playwright completes first-run → project → real deployment → service state → sign-out.
- [ ] The trusted-HTTPS integration deploys nginx, preserves a user route, and rebuilds a fresh
      SQLite application index while the stack remains running.
