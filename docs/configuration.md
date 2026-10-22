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
| `WINDLASS_DATA` | `/var/lib/windlass` | Platform state: the SQLite database and keys, projects, plugins. |
| `WINDLASS_PROJECTS` | `<data>/projects` | Where project directories live. Point it at an existing stacks directory to adopt what is already there. Local backups go in a `backups` directory beside it. |
| `WINDLASS_LOG_LEVEL` | `info` | One of `debug`, `info`, `warn`, `error`. An unrecognised value is a startup error rather than a silent fallback. |
| `WINDLASS_CADDY_ADMIN` | `http://127.0.0.1:2019` | Caddy admin API. Set this when Caddy runs in a container: `http://caddy:2019`. |
| `WINDLASS_PANEL_UPSTREAM` | `127.0.0.1:8080` | The address Caddy dials to reach the panel, used by the Settings-managed panel hostname. Change it when Caddy is not on the host network. |
| `WINDLASS_TRUSTED_PROXIES` | `127.0.0.0/8,::1/128` | Sources allowed to supply forwarding headers. The default covers the recommended local Caddy. Widen it only for a proxy you control. |
| `WINDLASS_UPDATE_REPO` | `Sulaiman-Dauda/windlass` | Repository checked for releases. |
| `WINDLASS_UPDATE_TOKEN` | unset | Authenticates release checks and downloads. Required only for a private update repository. Windlass removes it from its environment once read, so Compose, Git and plugins never see it. |
| `WINDLASS_NO_SELF_UPDATE` | unset | Any non-empty value disables self-update. Useful when a package manager or image tag owns the version. The panel still shows when a new version exists, without the Update now button. |

`WINDLASS_TRUSTED_PROXIES` decides whether a client-supplied `X-Forwarded-For` is believed.
Trusting an address you do not control lets a client claim any source IP, which matters
wherever an IP is used for a decision or an audit entry. The default trusts loopback only,
which is right for Caddy on the same host.

## Settings in the panel

These are not environment variables, and they are stored in SQLite. Credentials among
them are encrypted with `secret.key`.

- Panel domain, which creates the HTTPS route for the panel itself (Settings, General)
- Users, two-factor and GitHub or Google sign-in (Settings, Users and sign-in)
- Personal API tokens, stored as hashes (Settings, API tokens)
- GitHub App and Git connections (Settings, Git)
- Container registry credentials (Settings, Registries)
- The S3 backup destination and its keys (Settings, Backups)

## Files on disk

```text
/var/lib/windlass/
├── data/
│   ├── windlass.db      # platform state
│   ├── secret.key       # encryption key for stored credentials
│   ├── session.key      # signs session cookies
│   └── windlass.lock    # stops a second Windlass using the same data
├── projects/            # one directory per project
├── backups/             # local backup archives, beside projects/
└── plugins/             # installed plugins
```

`secret.key` decrypts stored credentials. A platform backup without it leaves those
credentials unreadable, and a copy of it in the wrong place hands them over. Back the data
directory up as a whole, and treat the archive as a secret.
