# Configuration reference

Windlass is configured entirely through `WINDLASS_*` environment variables, so it behaves
the same under systemd and under Docker. Everything else, the parts an operator changes
day to day, lives in Settings in the panel.

Under systemd, put overrides in a drop-in rather than editing the unit:

```sh
sudo systemctl edit windlass
```

```ini
[Service]
Environment=WINDLASS_LOG_LEVEL=debug
```

## Variables

| Variable | Default | What it does |
| --- | --- | --- |
| `WINDLASS_ADDR` | `:8080` | Listen address for the HTTP server. |
| `WINDLASS_DATA` | `/var/lib/windlass` | Platform state: SQLite database, secret key, projects. |
| `WINDLASS_PROJECTS` | `<data>/projects` | Where project directories live. Point it at an existing stacks directory to adopt what is already there. |
| `WINDLASS_LOG_LEVEL` | `info` | One of `debug`, `info`, `warn`, `error`. An unrecognised value is a startup error rather than a silent fallback. |
| `WINDLASS_CADDY_ADMIN` | `http://127.0.0.1:2019` | Caddy admin API. Set this when Caddy runs in a container: `http://caddy:2019`. |
| `WINDLASS_PANEL_UPSTREAM` | `127.0.0.1:8080` | The address Caddy dials to reach the panel, used by the Settings-managed panel hostname. Change it when Caddy is not on the host network. |
| `WINDLASS_TRUSTED_PROXIES` | `127.0.0.0/8,::1/128` | Sources allowed to supply forwarding headers. The default covers the recommended local Caddy. Widen it only for a proxy you control. |
| `WINDLASS_UPDATE_REPO` | `Sulaiman-Dauda/windlass` | Repository checked for releases. |
| `WINDLASS_UPDATE_TOKEN` | unset | Authenticates release checks and downloads. Required only for a private update repository. |
| `WINDLASS_NO_SELF_UPDATE` | unset | Any non-empty value disables self-update. Useful when a package manager or image tag owns the version. |

`WINDLASS_TRUSTED_PROXIES` decides whether a client-supplied `X-Forwarded-For` is believed.
Trusting an address you do not control lets a client claim any source IP, which matters
wherever an IP is used for a decision or an audit entry. The default trusts loopback only,
which is right for Caddy on the same host.

## Settings in the panel

These are not environment variables, and they are stored in SQLite:

- Panel hostname, which creates the HTTPS route for the panel itself
- OAuth sign-in providers
- GitHub App and Git connections
- Container registry credentials
- S3 backup endpoint and credentials
- Update checking

## Files on disk

```text
/var/lib/windlass/
├── windlass.db      # platform state
├── secret.key       # encryption key for stored credentials
└── projects/        # one directory per project
```

`secret.key` decrypts stored credentials. A platform backup without it leaves those
credentials unreadable, and a copy of it in the wrong place hands them over. Back the data
directory up as a whole, and treat the archive as a secret.
