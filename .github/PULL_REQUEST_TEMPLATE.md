## What this changes

<!-- What it does and, more importantly, WHY. What failure does it fix? -->

## How you verified it

<!--
Required. "It compiles" is not verification.

Say what you actually ran, e.g.:
  - `make test` passes
  - deployed a two-service stack, restarted Windlass, containers kept running
  - checked the Caddy config only contains windlass_* objects afterwards
-->

## Checklist

- [ ] `make test` and `go vet` pass
- [ ] The project filesystem stays authoritative for application config
- [ ] Deployed containers still survive Windlass stopping
- [ ] Privileged work stays inside `internal/agent`
- [ ] Caddy changes are targeted `@id` operations only
- [ ] `docs/` updated if behaviour changed
