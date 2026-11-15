# Panel benchmark: Windlass, Coolify and Dokploy

Run on 14/11/2026 on Vultr in London: fresh Ubuntu 24.04 servers, one per panel and size, each
installed with the project's own one-line installer. Windlass v0.62, Coolify 4.7.5, Dokploy
v0.31.2. Everything here can be run again with the scripts in this directory.

## Results

Memory is MiB, the median of 10 samples taken 30 seconds apart. "Added" is used memory plus swap
in use, minus the same server's fresh-OS reading before install.

### 2 GB servers (2 vCPU, Vultr vc2-2c-2gb)

Both Coolify and Dokploy list 2 GB as their minimum, and Coolify asks for 2 cores, so this is
the fair comparison.

| | Windlass | Coolify | Dokploy |
|---|---|---|---|
| Install time, one run | 87 s | 107 s | 190 s |
| Added memory, idle, no apps | 181 | 621 | 649 |
| The panel's own processes, idle | 78 | 551 | 520 |
| Used memory with the test app running | 542 | 1010 | 847, plus 183 in swap |
| Added disk after install | 1.1 GB | 2.7 GB | 5.1 GB |

"The panel's own processes" is Windlass, Caddy and the socket proxy for Windlass; every
`coolify*` container for Coolify (app, Postgres, Redis, Traefik proxy, sentinel, error pages);
every `dokploy*` container for Dokploy (app, Postgres, Traefik). Docker itself is left out of
that row for all three.

### 1 GB servers (1 vCPU, Vultr vc2-1c-1gb)

Below Coolify's and Dokploy's stated minimum. All three installed and served the test app.

| | Windlass | Coolify | Dokploy |
|---|---|---|---|
| Install time, one run | 88 s | 246 s | 214 s |
| Added memory, idle (RAM + swap) | 103 + 25 | 233 + 427 | 278 + 351 |
| Swap in use with the test app | 28 | 464 | 460 |

On 1 GB the kernel moves Coolify and Dokploy into swap, so read the RAM and swap columns
together. Vultr's image comes with a swap file (2.3 GB here); a server without one would have
had to fit in RAM or kill something.

### Panel stopped

The test app was deployed through each panel on a `sslip.io` hostname. Then the panel's own
processes were stopped so they stay down through a reboot, leaving its proxy running
(`stop-panel.sh`).

| | Windlass | Coolify | Dokploy |
|---|---|---|---|
| App serves with the panel stopped | yes | yes | yes |
| App serves after a reboot with the panel still stopped | **no** | yes | yes |

Windlass loses its routes when Caddy restarts. The Caddy package starts from
`/etc/caddy/Caddyfile`, and Windlass adds its routes through Caddy's admin API, so a reboot or
a Caddy restart drops them until Windlass is running again. Starting Windlass put them back in
2 seconds. `docs/life-without-the-panel.md` documents this. Coolify's Traefik and Dokploy's
Traefik read routes from container labels and files, so they come back on their own.

## What this does not show

- Install time is one run per server and depends mostly on how fast images download.
- Memory is a snapshot of quiet servers with one small app, not a load test.
- Coolify's API is off by default and its tokens are made in the UI. `deploy-coolify.sh` turns
  the API on and makes a root token through `artisan`, which is the same as the two settings.
  Coolify accepts its first account only in the 10 minutes after it starts, so it was
  restarted once before registering.
- Dokploy's installer pins Docker 28.5.0; the other two installed Docker 29.9.3.
- `dockerd` used 96 to 104 MiB on the Windlass servers against 53 to 78 on the others. Not
  investigated.

## Run it yourself

On each fresh server, as root, with this directory copied to `/root`:

```sh
sh measure.sh baseline 3 10 > baseline.csv
sh install.sh windlass            # or coolify, dokploy; writes /root/install.done
sleep 600; sh measure.sh idle 10 30 > idle.csv
# Coolify only: create the first account at :8000/register (register-coolify.py does it
# through an ssh tunnel), then run deploy-coolify.sh. Windlass and Dokploy need no browser.
cp app/compose.yaml /root/ && sh deploy-windlass.sh   # or deploy-coolify.sh, deploy-dokploy.sh
sleep 300; sh measure.sh app 10 30 > app.csv
sh stop-panel.sh windlass; curl -I http://$(cat /root/app-host)/
systemctl reboot   # then curl again
```

`summarise.py results/*.csv` prints the medians. The raw samples from this run are in
`results/`.
