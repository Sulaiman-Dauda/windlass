# 10 Staging reads

Status: built 13/11/2026 on `feat/staging-commits-env-keys` (v0.62 PR 6, S2 and S3, #79).
Not part of the MCP scaffold. Ships in v0.62.

## Goal

Promote says what it ships. The staging card lists the commits live does not have yet,
says when staging has fallen behind its branch, and names the `.env` keys staging has and
live lacks. Read-only: nothing here changes a project.

## Fills

- `internal/agent`: `HostAgent.GitLog` and `GitRemoteHead`, with `GitLogReq`,
  `GitCommit`, `GitLogResult`, `GitRemoteHeadReq`, `ErrUnknownCommit`,
  `ErrBranchNotFound`. Internal only; the plugin SDK is unchanged.
- `internal/agent/local/gitlog.go`: `git log from..to` in the project's `src/` checkout,
  never fetching; `git ls-remote -- url refs/heads/<branch>` with the token in the
  environment, as `GitSync` does.
- `internal/agent/fake`: `History` (one line of commits per project) and `RemoteHeads`.
- `internal/staging`: `Status` gains `commits`, `commits_more`, `branch_head`,
  `env_missing`. `projects.EnvironmentKey` names `WINDLASS_ENVIRONMENT`.
- Web: the staging card and the Promote confirm. OpenAPI follows.

## Touches

- `internal/agent/agent.go`
- `internal/agent/local/gitlog.go`, `gitlog_test.go`, `gitsync_test.go`
- `internal/agent/fake/fake.go`
- `internal/projects/staging.go`
- `internal/staging/service.go`, `status_test.go`
- `api/openapi.yaml`
- `web/src/api/staging.ts`
- `web/src/components/StagingCard.tsx`
- `web/e2e/staging-promote.spec.ts`, `staging-recreate.spec.ts`

## Decisions

- Commits run from live's running commit to staging's verified one, capped at 50. Kept
  per staging project until the range changes, an unknown commit included. A commit the
  checkout lacks (force push, unrelated branches) leaves the list out.
- The branch head is asked in the background, at most once a minute per project and one
  ask at a time, under a 15 second deadline that covers minting a GitHub App token. A
  poll returns the last answer and never waits. A failed ask hides the notice for the
  minute.
- "Behind" compares the head with staging's latest deployment, and only once that
  deployment has finished.
- `env_missing` is key names only, ignores `WINDLASS_ENVIRONMENT`, and is a warning, not a
  blocker. A `.env` that cannot be parsed leaves it out, and its error is not logged
  because a parse error can quote part of a value.
- The title tooltip on the disabled Promote button is gone; the card already states the
  reason.
- The staging password (S1) is not in this unit.

## Done means

`make test`, `make lint`, `scaffold-check`, vitest and the web build pass. Unit tests with
the fake agent for commits (ahead, level, unknown, capped, cached), branch head (first
poll, cache, expiry, failure, one ask at a time) and env keys (missing, none, unparsable,
no values in the JSON). A real-git test for both agent methods, and the token test covers
`ls-remote`. Playwright: card, confirm, behind with Deploy, viewer without it.
Screenshots in light and dark. A fresh-context review.
