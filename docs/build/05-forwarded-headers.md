# 05 Forwarded headers

Status: built 10/11/2026 on `fix/forwarded-headers`, after #95 (painbaba, fixes #89) merged.
Not part of the MCP scaffold. Ships in v0.61.

## Goal

One place decides who the client is and whether it reached Windlass over HTTPS: the server's
`trustedRealIP` middleware. Nothing after it reads a forwarding header the client could have
written, and a plugin is told Windlass's view and nothing else.

## Fills

- `internal/server/server.go`: `trustedRealIP` makes the HTTPS decision on the address that
  connected and records it. Only the last `X-Forwarded-Proto` value counts.
- `internal/api/proxyproto.go`: the context key is private; `SetForwardedProto` writes the
  decision and `ForwardedProto` reads it. `internal/server/proxyproto.go` is removed.
- `internal/api/auth.go`: `remoteIP` no longer cuts the last group off a bare IPv6 address.
- `internal/plugins/service.go`: the plugin proxy rewrites the outbound request. It removes
  the client's forwarding headers and sets `X-Forwarded-For` and `X-Forwarded-Proto` from a
  `Client` value the API handler passes to `Proxy`.
- `docs/install.md` and `plugins-sdk/README.md`.

## Decisions

Settled during the build 10/11/2026, flagged for Pablo:

- **Last value, not first.** A proxy that appends its own `X-Forwarded-Proto` leaves the
  client's value on the left. Caddy replaces the header, so the recommended install was not
  exposed. `https, http` is plain HTTP, as it was.
- **Rewrite, not Director.** Go's reverse proxy drops headers named in `Connection` after a
  Director runs and before a Rewrite runs. Set in a Director, a client could have the
  headers removed again.
- **`Proxy` takes a `Client`.** The plugins package cannot import the API, and passing the
  decision in request headers would make those headers a contract nobody can see.
- **The removal list is a denylist.** It covers `Forwarded`, `X-Forwarded`, `X-Forwarded-*`,
  `X-Real-IP` and six common client-address headers, in hyphen or underscore spelling. A
  header not on it still passes. The SDK README tells plugin authors to read only the two
  headers Windlass sets.
- **This changes what plugins receive.** A plugin that read `X-Forwarded-Host`, or expected
  a chain in `X-Forwarded-For`, gets less than before. The SDK README says so under v0.61.
- **Container installs.** A Caddy that is not on loopback is untrusted until
  `WINDLASS_TRUSTED_PROXIES` names it. That came with #95. `docs/install.md` now says which
  one address to trust and why not a network.
- **Audit format.** A direct IPv6 client is recorded as `2001:db8::1`, no longer
  `[2001:db8::1]`.

## Review follow-up

A fresh Opus pass on the merged code (10/11/2026, after #111) gave four leads. Each was
checked against the code with a test that failed on main, then fixed on
`fix/forwarded-headers-review`:

- **Every `X-Forwarded-For` line is read.** `trustedRealIP` read only the first line. Behind
  a proxy that adds a line of its own, such as HAProxy's `option forwardfor`, the first line
  is the client's, so a client could choose the address in the audit log, the rate limiter
  and the plugin's `X-Forwarded-For`. Caddy appends to one line, so the recommended install
  was not exposed. Older than #95.
- **The plugin's query string is left alone.** In Rewrite mode Go re-encodes a query that
  holds a `;` or a bad escape and drops what it cannot parse: `a=1;b=2` and `q=50%` reached
  the plugin empty. #111 introduced that. The raw query is copied across again.
- **Ten more header names are removed**: scheme claims (`X-Scheme`, `X-Url-Scheme`,
  `Front-End-Https`), address claims (`Fastly-Client-IP`, `X-Envoy-External-Address`,
  `X-Remote-Addr`, `X-Remote-IP`, `X-ProxyUser-IP`) and host claims (`X-Original-Host`,
  `X-Host`). Still a denylist.
- **Forwarding names are dropped from request trailers.** Go passed the name on with no
  value, so nothing could be claimed that way; it is removed all the same.

A second fresh pass, on this follow-up, found three more. Each was confirmed with a failing
test first:

- **An `X-Forwarded-For` entry that is not an address ended the trust, not the walk.** The
  walk skipped it and took the entry to its left, which the client wrote. A proxy that
  appends `203.0.113.5:4411`, a bracketed IPv6 address or `unknown` gave the client its pick
  of address. Caddy writes a bare address, so the recommended install was not exposed. An
  address with a port or brackets is now read; anything else stops the walk and the address
  that connected stands.
- **`X-Real-IP` was believed beside `X-Forwarded-For`.** When every `X-Forwarded-For` entry
  was a trusted proxy, or none parsed, the client's own `X-Real-IP` was used, and Caddy does
  not remove one. It is now read only when there is no `X-Forwarded-For` at all, from its
  last line. With only trusted entries the farthest one is the client. This changes one
  setup: a proxy that sends both headers and puts the real client only in `X-Real-IP`.
- **22 more header names are removed**, among them `X-Original-URL` and `X-Rewrite-URL`,
  which some frameworks route on in place of the request line, and the `HTTP_` spelling.
  Still a denylist.

Known and left: a request trailer's value never reaches a plugin, only its name, because
Go's reverse proxy copies the trailer map before the body is read. That is older than #111.

## Touches

`internal/server/server.go`, `internal/server/proxyproto.go`,
`internal/server/proxyproto_test.go`, `internal/api/proxyproto.go`, `internal/api/auth.go`,
`internal/api/plugins.go`, `internal/api/plugins_test.go`, `internal/plugins/service.go`,
`internal/plugins/service_test.go`, `docs/install.md`, `plugins-sdk/README.md`,
`docs/build/05-forwarded-headers.md`

## Done when

- [x] `go vet ./...` and `go test ./... -count=1` pass (Pablo's run, 10/11/2026)
- [x] One Opus pass and one Codex pass on each half, findings applied
- [x] CI green on #111, the integration job included
- [x] The tests fail with each fix removed in turn: the trust check, the last-value rule, and
  the plugin header rewrite
- [x] A sign-in through a built binary on loopback: `Secure` and HSTS with
  `X-Forwarded-Proto: https` from a trusted address, neither from an untrusted one, neither
  when the last of two values is `http`
- [x] A real plugin that prints its request headers, behind the proxy: it saw the two
  headers and none of seven spoofed ones, `Connection` naming both included
- [x] Behind a real Caddy 2.11.8 with main at 35a398b (10/11/2026): `Secure` and HSTS over
  HTTPS, neither over HTTP, a client's own `X-Forwarded-Proto` ignored both ways, and neither
  once Caddy's address is not in `WINDLASS_TRUSTED_PROXIES`
- [x] Review follow-up: the four new tests fail on main and pass on the branch
