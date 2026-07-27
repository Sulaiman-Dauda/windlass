# Security

Windlass is a control plane for Docker Compose. It holds Docker socket access,
writes Caddy configuration and stores platform credentials, so a compromise of
Windlass is a compromise of everything it deploys. Treat it accordingly.

## Status — read this first

Windlass is **pre-1.0 and has had no external security audit**. It is suitable
for machines you control and can rebuild. Do not put it in front of
infrastructure you cannot afford to lose.

## Reporting a vulnerability

**Do not open a public issue.**

Use [Security → Report a vulnerability](../../security/advisories/new), which is
private to the maintainer. Please include:

- what an attacker can do, not only what looks wrong
- the steps to reproduce it, ideally against a throwaway host
- the version or commit you tested

You will get an acknowledgement within a few days. Windlass is maintained by one
person alongside other work; a fix for something serious takes priority over
everything else here.

## The trust boundary

The design assumption is that **privileged work is confined to one package**:

- Only `internal/agent/local` touches Docker, Caddy, project files, Git or exec.
  Everything else goes through the agent interface.
- Compose operations shell out to the real `docker compose` rather than
  reconstructing container specs, so behaviour matches what an operator would
  get by hand.
- Deployed containers keep running if Windlass stops or is removed. It is a
  control plane, not a runtime dependency.

A way to cross that boundary — to reach Docker or the host from outside
`internal/agent/local` — is the most valuable thing anyone could report.

## In scope

- Privilege escalation from the web UI or API to the host
- Authentication or session bypass
- One project reading or modifying another project's files, env or containers
- Caddy configuration injection that redirects or intercepts traffic
- Secrets (`.env` values, tokens) exposed in logs, API responses or the UI

## Not in scope

- Anything requiring existing root on the host
- Denial of service by an authenticated administrator
- Findings from running Windlass with `--dangerously-*` style overrides
- Missing hardening on a machine you have deliberately exposed to the internet
