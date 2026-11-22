# Alerts

Windlass can tell you when something needs a person, instead of waiting for you to open the
panel. Alerts go to channels you set up under **Settings, Alerts**. Only admins see that page.

## Channels

| Kind | What you give it |
| --- | --- |
| **Webhook** | A URL and a signing secret of at least 16 characters. Each alert is a JSON `POST`, signed as below. |
| **ntfy** | A topic, and a server if you run your own (ntfy.sh otherwise). An access token for a protected topic. |
| **Discord** | A channel webhook URL (channel settings, Integrations, Webhooks). |
| **Slack** | An incoming webhook URL. |
| **Email** | An SMTP server, port, security (STARTTLS, TLS or none), optional username and password, a From address and one or more To addresses. |

Each channel takes the events you pick, or every problem if you pick none, and every project
or one. A successful deploy is not a problem: a channel gets it only if you pick it. A channel
that is off keeps its settings, and its alerts wait until you turn it back on (up to 30 days).

**Send test** sends a test alert at once and shows the real error if it fails, so a wrong
URL or password shows on the screen.

Secrets (the webhook signing secret, the ntfy token, the SMTP password, and the Discord and
Slack URLs, which are credentials themselves) are stored encrypted with `data/secret.key`
and never returned by the API. When you edit a channel, an empty secret field keeps the
stored value. Changing where a secret is sent (the ntfy server, or the SMTP server, port,
security or username) clears the stored token or password, so enter it again: an edit must
not send an old secret to a new server.

## Delivery

Alerts go through an outbox in the database, so an alert raised while a channel is down is
not lost, and survives a restart of Windlass. A failed send is tried again after 1, 5 and 15
minutes, then 1 and 3 hours: six attempts over about five hours. A refusal that sending
again cannot fix (a 4xx answer other than 408 or 429, or a refused address) stops at once.
A channel that fails is skipped for the rest of that pass, so one dead receiver does not
hold up the others. **Recent alerts** on the same page shows the last 20 deliveries and why any failed. Rows are
kept for 30 days.

A failed alert never fails the deploy, backup or check that raised it.

Alerts carry the event, the project name, a title and a short message. They never carry env
values or file contents.

## Addresses Windlass will not send to

Webhook, ntfy, Discord and Slack sends are refused when the address resolves to a loopback,
private (10/8, 172.16/12, 192.168/16, fc00::/7, fec0::/10), shared (100.64/10, which
includes Tailscale and some cloud metadata services), 192.0.0/24, 198.18/15, link-local (169.254/16, fe80::/10),
multicast or unspecified address. The check runs on the address being connected to, after
DNS, so a name cannot pass the check and then resolve somewhere else. Redirects are not
followed, and proxy variables are ignored.

This keeps a webhook from being aimed at the Docker socket proxy (127.0.0.1:2375), the Caddy
admin API (127.0.0.1:2019) or a cloud metadata service. Run your receiver on a public address
or use ntfy.sh.

Email may use a relay on the same server (127.0.0.1 or ::1), because that is the common
self-hosted setup. A username needs a password, and a password is refused with security set to none, so it is never sent in
the clear.

## Verifying a webhook

Each request has these headers:

| Header | Value |
| --- | --- |
| `X-Windlass-Event` | The event kind, for example `deploy_failed` |
| `X-Windlass-Timestamp` | Unix seconds when the alert was raised |
| `X-Windlass-Signature` | `sha256=` and the hex HMAC-SHA256 of `<timestamp>.<body>` with your secret |

Recompute the HMAC over the timestamp, a full stop and the raw body, compare it in constant
time, and refuse a timestamp more than a few minutes old. The timestamp is the time the
alert was raised, so a retried alert keeps its original timestamp; allow for the retry
schedule above if you check age strictly.

```python
import hashlib, hmac, time

def verify(secret: bytes, headers, body: bytes, max_age=6 * 3600) -> bool:
    ts = headers["X-Windlass-Timestamp"]
    want = "sha256=" + hmac.new(secret, ts.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, headers["X-Windlass-Signature"]) and time.time() - int(ts) < max_age
```

The body:

```json
{
  "event": "deploy_failed",
  "project": "shop",
  "title": "Deploy failed",
  "message": "Deployment 14 stopped at pulling: manifest unknown",
  "time": "2026-11-22T10:00:00Z"
}
```

`recovered: true` is added on the message that says an incident has cleared.

## API

The routes are under `/api/v1/notifications` and are admin only. Reading channels and
deliveries works with an admin API token. Adding, changing, deleting and testing a channel
needs a browser session, like the S3 destination: a leaked token must not be able to send
your events somewhere else. Every change and test is in the audit log. There is no MCP
tool for channels. See [API](./api.md).
