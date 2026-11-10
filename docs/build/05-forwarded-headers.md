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
- [ ] Not run behind a real Caddy with this build; CI's integration job covers Caddy routing,
  not sign-in cookies
