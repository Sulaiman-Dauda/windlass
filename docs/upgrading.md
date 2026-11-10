# Upgrading

What to check when you move to a new version. A version not listed here needs nothing.

## v0.61

Windlass now believes forwarded headers only from a reverse proxy it has been told about,
and passes fewer of them to plugins. The recommended install, Caddy on the same server as
Windlass, needs no change. Check the cases below if yours differs.

### A reverse proxy that is not on the same host

This is the widest case: Caddy in a container, or a proxy on another machine.

`WINDLASS_TRUSTED_PROXIES` defaults to loopback. Before v0.61 it covered the client's address
only, and `X-Forwarded-Proto` was believed from anyone. Now it covers both. A proxy outside
the list is ignored, so Windlass treats every request as plain HTTP:

- sign-in cookies are not marked `Secure` and no `Strict-Transport-Security` header is sent
- OAuth callback, GitHub App and webhook URLs are built with `http://`, which a provider
  that has the `https://` address registered can refuse

As before v0.61, the audit log and the sign-in rate limit also see such a proxy's address
for every visitor.

Windlass logs a warning the first time it happens, with the address to add:

```sh
sudo journalctl -u windlass | grep WINDLASS_TRUSTED_PROXIES
```

```text
{"level":"WARN","msg":"forwarding headers ignored: this address is not in WINDLASS_TRUSTED_PROXIES. ...","address":"10.89.5.2"}
```

In the container install, read it with `docker compose -f docker-compose.install.yaml logs windlass`.

If that address is your proxy, add it and restart:

```sh
sudo systemctl edit windlass
```

```ini
[Service]
Environment=WINDLASS_TRUSTED_PROXIES=127.0.0.0/8,::1/128,10.89.5.2
```

In the container install, set the same variable under `environment:` for the `windlass`
service.

Trust the one address, never a whole Docker network: every container on a trusted network
can claim any client address and claim HTTPS. [Install](./install.md) has the detail for the
container layout. The warning is logged once for each address, for private addresses only,
and for at most 16 of them between restarts.

### Plugins

Windlass sets `X-Forwarded-For` and `X-Forwarded-Proto` on a request to a plugin and removes
what the client sent in `Forwarded`, `X-Forwarded`, every `X-Forwarded-*` header and 39 other
names, among them `X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`, `X-Original-URL` and
`X-Rewrite-URL`. A plugin that reads `X-Forwarded-Host`, expects a chain of addresses in
`X-Forwarded-For`, or takes the client's address from a CDN's header gets less than before.
Read the address and scheme from the two headers Windlass sets. The
[plugin SDK](../plugins-sdk/README.md) has the contract.

### A proxy that sends `X-Real-IP`

`X-Real-IP` is read only when the request has no `X-Forwarded-For` at all. A proxy that
sends both and puts the real client only in `X-Real-IP` now has its `X-Forwarded-For`
believed instead. Have it send the client's address in `X-Forwarded-For`.

### A proxy that writes something other than an address

When the entry a trusted proxy added to `X-Forwarded-For` is not an address, `unknown` for
one, Windlass stops there and records the proxy's own address. An address with a port or in
brackets is read as before.

### Audit log

A client that connects directly over IPv6 is recorded as `2001:db8::1`, no longer
`[2001:db8::1]`.
