# 07 Adoption guard

Status: built 13/11/2026 on `feat/adoption-guard` (v0.62 PR 3, D0). Not part of the MCP
scaffold. Ships in v0.62.

## Goal

Adopting a stack that is already running never quietly swaps its data for empty volumes.
A scan also says which directories it passed over and why, instead of skipping them in
silence.

## Fills

- `internal/agent`: `Container.ComposeWorkingDir` from the
  `com.docker.compose.project.working_dir` label; `FSAgent.SkippedProjects` and the shared
  `SkipReason` rule. Internal only; the plugin SDK is unchanged.
- `internal/projects/adoption.go`: `FirstDeployConflict`, `AdoptionConflicts`, `Skipped`,
  and `AdoptionError` with a message that names the new volumes and the fix.
- `internal/deploy/pipeline.go`: `deploy()` refuses before a deployment row is written.
- API: deploy and Git webhook answer 409 `adoption_conflict`; scan answers
  `{count, skipped, conflicts}`. OpenAPI and `docs/projects-and-compose.md` follow.
- Panel: a scan report dialog and a "Deploy not started" dialog.

## Touches

- `internal/agent/`
- `internal/projects/adoption.go`
- `internal/deploy/pipeline.go`
- `internal/deploy/adoption_test.go`
- `internal/api/deployments.go`
- `internal/api/git.go`
- `internal/api/projects.go`
- `internal/server/adoption_api_test.go`
- `api/openapi.yaml`
- `docs/projects-and-compose.md`
- `web/src/api/projects.ts`
- `web/src/components/AdoptionReport.tsx`
- `web/src/pages/Projects.tsx`
- `web/src/pages/ProjectDetail.tsx`

## Decisions

- A container belongs to a directory by the last element of its working directory, so a
  stack copied in from `/srv/<name>` is caught as well as one started in place.
- The guard holds until the project's first successful deploy, so a failed first attempt
  does not turn it off.
- A container running as a project Windlass manages is never a conflict. Compose keeps the
  old working_dir label after a rename, and a new project reusing that name must not be
  blocked.
- No "deploy anyway" override. Starting fresh means removing the old stack with
  `docker compose -p <old> down`, which keeps its volumes.
- `docker-compose.yml` is still not discovered, only named; accepting it is a wider change.
- Not covered: a project whose directory is renamed after Windlass deployed it.

## Done means

`make test`, `make lint`, `scaffold-check`, `make test-integration` (dump tests need `,z`
under SELinux locally), and a real run: a hand-started stack blocks the first deploy with
no new volume created, the rename fix deploys with the data kept, and a new project reusing
the old name deploys.
