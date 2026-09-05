# Screenshots

Captured from a real panel at 1440x900, device scale factor 2, in light and dark. Each
view is shot in both themes so pages can pair them to the reader's preference.

| View | Light | Dark |
| --- | --- | --- |
| Dashboard, host metrics and projects | `dashboard-light.png` | `dashboard-dark.png` |
| Projects list | `projects-light.png` | `projects-dark.png` |
| Project overview, services and limits | `project-light.png` | `project-dark.png` |
| Deployments, history and streaming log | `deployments-light.png` | `deployments-dark.png` |
| Domains and automatic HTTPS | `domains-light.png` | `domains-dark.png` |
| Logs | `logs-light.png` | `logs-dark.png` |
| Templates | `templates-light.png` | `templates-dark.png` |
| Settings | `settings-light.png` | `settings-dark.png` |

These are real deployments, not mockups: five Compose projects running nginx, PostgreSQL,
Uptime Kuma, linkding and n8n, with Caddy in front and three domains routed.

## Recapturing

Screens drift. When one does, reshoot the affected pair rather than editing the image.
The capture asserts it is signed in and that a view-specific selector rendered before
taking each shot, because a Playwright script that logs in and waits for network idle
will happily capture the login page over and over: every file identical, every one wrong.
Checksum the results and confirm they differ.
