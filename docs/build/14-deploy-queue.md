# 14 Deploy queue

Status: built 21/11/2026 on `feat/v063-u11-deploy-queue` (v0.63 U11, F8, #70). Fills the
`cancel_deployment` tool and the `get_project` queue field agreed in ARCHITECTURE.md
"Additions in v0.63". Ships in v0.63.

## Goal

A deploy asked for while another deployment of the project runs waits and starts when it
finishes, instead of a 409 (manual) or being skipped (webhooks). Choice 2 (a): in memory,
lost on restart with the reason logged, no migration. Rollback and promote keep their 409.

## Fills

- `internal/deploy/queue.go`: `DeployOrQueue`, `WaitingFor`, `CancelWaiting`,
  `DropWaiting`, and `startWaiting`, deferred in `runJob`. One slot per project; repeat
  requests add to `requests`. `queueMu` covers the active check and row creation in
  Deploy, Rollback, Promote and DeployFirst.
- `internal/api/deployments.go`: POST answers 202 `{waiting}`; `GET` and `DELETE
  /projects/{name}/deployments/waiting` (DELETE in `RequireDeployer`, audited).
- Webhooks (`git.go`, `githubapp.go`) queue and audit `webhook.queue`.
- MCP: `deploy` answers `status: waiting`; `cancel_deployment`; `get_project`
  `waiting_deploy`.
- Panel: Waiting row with Cancel in the Deployments history; the three Deploy buttons
  handle the 202.

## Touches

- `internal/deploy/`, `internal/api/`, `internal/mcp/`, `cmd/windlass/main.go`,
  `internal/agent/fake` (`UpGate`, test only), `api/openapi.yaml`, `docs/`, `web/src/`

## Leaves alone

The jobs table, migrations, rollback and promote semantics, the deployment state machine.

## Done means

`internal/deploy/queue_test.go`, `internal/server/deploy_queue_api_test.go` and
`TestDeployWaitsAndCancels` pass under `-race`; a local instance folds requests made during
a run into one waiting deploy that starts after it; Cancel removes it.
