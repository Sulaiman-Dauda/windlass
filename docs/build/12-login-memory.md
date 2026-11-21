# 12 Login memory

Status: built 21/11/2026 on `v063-u7-login-memory` (v0.63 U7). Not part of the MCP
scaffold. Ships in v0.63.

## Goal

The panel's RSS returns near idle within seconds of a login. The argon2 hash holds 19 MiB
while it runs, and the Go runtime kept that freed memory for about two minutes.

## Fills

- `internal/auth/password.go`: `scheduleRelease`, called after each hash, runs
  `debug.FreeOSMemory` 5 s after the first hash of a burst. Later hashes neither push it
  back nor start another, so a flood forces at most one GC per 5 s.

## Touches

- `internal/auth/`

## Leaves alone

Hash parameters, verification, the hash slots. The update check's CA roots (about 9 MiB,
peak near 32 MiB) are under the 40 MiB budget and left to the runtime.

## Done means

Local instance: idle 23 MiB; login plus dashboard 54 MiB before, 29 MiB within 8 s after.
`go test -race ./internal/auth/` passes.
