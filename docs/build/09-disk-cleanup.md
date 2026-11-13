# 09 Disk clean-up

Status: built 13/11/2026 on `feat/disk-cleanup` (v0.62 PR 5, #122). Not part of the MCP
scaffold. Ships in v0.62.

## Goal

A host that builds stops filling its disk without anyone visiting Settings. Unused images
and build cache are cleared after each successful deployment, the cache is trimmed
wherever BuildKit keeps it, and a run that removed nothing says why.

## Fills

- `internal/agent`: `DockerAgent.PruneBuildCache`; `ImagePruneResult.Failed` and
  `Errors`; `DiskUsage` counts `buildx_buildkit_*_state` volumes as build cache. Internal
  only; the plugin SDK is unchanged.
- `internal/agent/local`: `PruneImages` lists with `Verbose` (Docker 29 lists no images
  without it), counts failures, forces an unused image under two or more tags.
  `PruneBuildCache` runs Docker's build prune, then `docker buildx prune --keep-storage`
  when a builder container is running, and names a stopped or extra builder.
- `internal/cleanup`: settings, the run, the last result, `AfterDeploy`.
- `internal/deploy`: `Service.OnSucceeded`, called after a deployment succeeds.
- `internal/store/db/reconcile.go`: `ActiveDeploymentProjects`. No migration.
- API: `GET` and `PUT /system/docker/cleanup`, `POST /system/docker/cleanup/run`, admin
  only. `/system/disk` re-reads after a run. OpenAPI follows.
- Web: the Docker storage card in Settings > System.

## Touches

- `internal/agent/`
- `internal/cleanup/`
- `internal/deploy/pipeline.go`
- `internal/deploy/pipeline_test.go`
- `internal/store/db/reconcile.go`
- `internal/api/cleanup.go`
- `internal/api/api.go`
- `internal/api/attention.go`
- `internal/server/cleanup_api_test.go`
- `internal/server/server_test.go`
- `cmd/windlass/main.go`
- `api/openapi.yaml`
- `web/src/pages/Settings.tsx`
- `scaffold.json` (ignore list)

## Decisions

- 13/11/2026 Pablo: trim by shelling out to `docker buildx prune`. The installer's proxy
  leaves `GRPC=0`, so buildx falls back to the `buildx_buildkit_default` container on every
  install. Allowing `/grpc` in the proxy may come in v0.65.
- 13/11/2026 Pablo: automatic run on by default, 7 days, 5 deployments, 10 GB cache.
- Settings keys `disk_cleanup` and `disk_cleanup_last`. `PUT` requires every field.
- No run while a deployment queued in the last 6 hours is running: a pulled image has no
  container until `up`. The 6 hours stops a row stuck after a crash holding it off. An
  automatic run records the skip; a manual one returns 409.
- A failed deployment cleans nothing. Each step runs even if the other fails.
- `POST /system/docker/images/prune` is left as it was for API clients.

## Done means

`make test`, `make lint`, `scaffold-check`, `make test-integration`, and
`TestCleanupRealDocker` on Docker 29 both direct and through the installer's proxy: the
old image is removed, the running one kept, the cache trimmed, a stopped builder named. A
real panel: manual run, save, 409 during a deployment, and under 40 MiB.
