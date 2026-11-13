# 08 Readouts

Status: built 13/11/2026 on `feat/v062-readouts` (v0.62 PR 4: P6, P7 with F11 and the read
half of F7, F3, F5). Not part of the MCP scaffold. Ships in v0.62.

## Goal

The dashboard can say what needs the operator without them hunting for it: failed deploys,
a filling disk, data with no backup, Caddy down, an update. It also shows what the panel
itself costs in memory, where Docker's disk goes, whether a project runs what its files say,
and every service's logs in one stream. Everything here only reads.

## Fills

- `internal/agent`: `ComposeAgent.ConfigHashes`, `DockerAgent.DiskUsage` and `ImageID`;
  `Container.ConfigHash`, `ImageID`, `Volumes` (named only); `LogOpts.Since`; log lines
  carry Docker's timestamps. Internal only; the plugin SDK is unchanged.
- `internal/attention`: the list, its rows and tones, the 30s shared cache, the background
  update check.
- `internal/selfstat`: the panel's resident memory and a 60-sample ring, one a minute.
- `internal/deploy/drift.go`: `Service.Drift`.
- `internal/store/queries/deployments.sql`: `ListLatestFailedDeployments`, the latest
  finished deploy per project when it failed. No migration.
- API: `GET /system/attention`, `/system/disk`, `/projects/{name}/drift`; `panel` in
  `/system/metrics`; merged logs when no `service` is given. OpenAPI follows.

## Touches

- `internal/agent/`
- `internal/attention/`
- `internal/selfstat/`
- `internal/deploy/drift.go`
- `internal/deploy/drift_test.go`
- `internal/store/queries/deployments.sql`
- `internal/store/db/deployments.sql.go`
- `internal/api/attention.go`
- `internal/api/api.go`
- `internal/api/logs.go`
- `internal/api/metrics.go`
- `internal/api/projects.go`
- `internal/server/readouts_api_test.go`
- `cmd/windlass/main.go`
- `api/openapi.yaml`
- `scaffold.json` (ignore list)

## Decisions

- Every route is viewer level, as `/system/metrics` and logs are. The update row goes to
  admins only, as `/system/update` does. `/system/disk` gives counts and bytes, no names.
- The list is built at most every 30 seconds and shared, apart from the request that asked,
  with a 20 second limit. A row can outlive its fix by 30 seconds.
- Disk threshold: settings key `attention_disk_percent`, default 85, 50 to 99. No screen yet.
- No backup row for staging projects, or for anonymous volumes (a 64 hex name).
- The Caddy row only when a project or the panel has a domain.
- Drift under a live rollback hashes with `compose.rollback.yaml` and skips the image check,
  since the images are pinned by digest.
- Merged logs: at most 5000 lines of history, then each container followed from its last
  line sent, dropping anything not newer. Podman repeats the boundary line otherwise.
- No prune here: that is PR 5 (#122).

## Done means

`make test`, `make lint`, `scaffold-check`, `make test-integration` (dump tests need `,z`
under SELinux locally), and a real run on scratch projects: a failed pull and a volume with
no schedule appear in the list, drift shows after a file edit, merged follow of two services
has no gaps or repeats, and the panel stays under 40 MiB.
