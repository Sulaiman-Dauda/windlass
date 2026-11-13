# 06 Update provenance

Status: built 10/11/2026 on `feat/update-provenance` for #90. Not part of the MCP scaffold.
Held for v0.62 on Pablo's decision of 10/11/2026: it adds a dependency and memory, see Cost,
and the review fixes have had no outside pass yet.

## Goal

The panel's self-updater installs a binary only when GitHub holds build provenance for those
exact bytes and it verifies. `checksums.txt` lives in the release beside the binary, so it
catches a broken download and nothing else. On failure the update is refused and the binary
deleted. `WINDLASS_NO_SELF_UPDATE` stays the way out.

## Fills

- `internal/update/provenance.go`: `verifyProvenance` fetches the Sigstore bundles for the
  binary's SHA-256 from GitHub's attestations API and verifies them with `sigstore-go`.
  `checkBuild` then checks the statement type, the asset name, source repository, tag and
  runner.
- `internal/update/update.go`: `fetchVerified` holds the download, the checksum and the
  provenance check, and deletes the file when any of them fails. `Apply` only swaps, one
  call at a time. `Check` does not offer a release older than the running version.
- `internal/update/testdata/`: the real v0.60 linux/amd64 attestation and a snapshot of
  Sigstore's trusted root, so the tests run offline.
- `Dockerfile`, `CONTRIBUTING.md`, `AGENTS.md`: Go 1.27, which `sigstore-go` v1.4.0 requires.
- `docs/security.md`, `docs/install.md`, `docs/configuration.md`.

## What is checked

1. A certificate transparency proof on the signing certificate, one transparency log entry,
   and the certificate valid at the time that log recorded the signature.
2. The statement names the SHA-256 of the file on disk.
3. Issuer `https://token.actions.githubusercontent.com`, and the certificate's subject is
   `https://github.com/<repo>/.github/workflows/release.yaml@refs/tags/<version>`. The
   repository compares without case, the tag exactly.
4. Predicate `https://slsa.dev/provenance/v1`, source repository and ref equal to the update
   repository and tag, runner `github-hosted`.
5. The statement lists those bytes under the asset name for this architecture,
   `windlass-linux-<arch>`. One statement covers both binaries of a release and step 2
   accepts either, so without this the arm64 binary served as the amd64 asset would pass.

## Decisions

Settled during the build, flagged for Pablo:

- **Three modules named in `go.mod`.** `github.com/sigstore/sigstore-go` verifies.
  `github.com/theupdateframework/go-tuf/v2` was already pulled in by it and is imported
  directly for the fetcher that carries the context. `github.com/in-toto/attestation`, also
  already pulled in, is imported by the tests to build a statement.
- **`sigstore-go`, not our own verifier.** Doing this by hand means writing certificate
  transparency, Rekor and DSSE verification ourselves, in the one code path that replaces a
  binary running as root. `gh attestation verify` uses the same library.
- **Keys fetched at update time.** Sigstore's trusted root comes over TUF from
  `tuf-repo-cdn.sigstore.dev`, starting from the TUF root compiled in. A snapshot baked into
  a release would go stale when Sigstore rotates a key and block every update from that
  version. Nothing is written to disk. The TUF client still reads cached metadata when its
  cache directory holds some, and its default is `$HOME/.sigstore` or the system temp
  directory, so it is pointed at `<data>/update/sigstore`, which Windlass never creates.
  The fetch carries the request's context and a 60 second limit.
- **The tag is pinned.** A genuine v0.60 binary published as v0.61 is refused.
- **An older release is not an update.** A genuine older release marked as the latest has
  valid provenance for its own tag, so the tag pin does not stop it. `Check` compares dotted
  version numbers and offers nothing lower than the running version. A version that is not
  dotted numbers (`dev`, `v0.61-rc1`) is never treated as older.
- **One install at a time.** A second `Apply` while one runs is refused; it would download
  over the file the first had verified.
- **Refuse, never skip.** No attestation, GitHub or Sigstore unreachable, or a bundle that
  does not parse: all refuse.
- **Private update repositories are refused.** GitHub signs them with its own Sigstore
  instance, which needs a second trusted root. Not built, and not measured how many installs
  set `WINDLASS_UPDATE_TOKEN`.

## Cost

Measured 10/11/2026, linux/amd64, CI's build flags without the embedded frontend, three runs
each, against main at 35a398b:

| | main | this branch |
|---|---|---|
| Binary | 27.8 MB | 40.5 MB |
| Binary, stripped (`-s -w`) | 19.0 MB | 26.8 MB |
| Idle RSS | 21.4 to 23.2 MB | 30.6 to 31.4 MB |
| Modules linked in | 32 | 86 |

About 9 MB of idle memory against the 40 MiB budget, for code that runs when someone presses
Update now. Package initialisers account for 1.8 MB of it; the rest is the larger binary.

## Review

Two passes on the first commit by reviewers that did not write it. One found nothing. The
other's leads, each checked against the code, became the asset name check, the older release
rule, the install lock, the TUF cache directory, the context on the TUF fetch and
`per_page=100` on the attestations request.

## Checks

- `go test ./internal/update/`: the real v0.60 attestation passes for amd64 and arm64; other
  bytes, another tag, another repository, the arm64 binary under the amd64 name, a statement
  edited after signing, a missing log entry, a trusted root with no certificate transparency
  log, no attestation and no Sigstore keys are each refused; a refused binary is not left on
  disk; an older release is not offered; a second `Apply` is refused.
- Removing any one of these fails the tests: the provenance call, the `checkBuild` call, the
  asset name, the subject digest, the older release rule, the install lock, the tag from
  both places it is checked.
- Live, 10/11/2026: the v0.60 binary verified against GitHub and Sigstore in 1.0 s; the same
  binary with one byte appended was refused; the same binary as v0.61 was refused.
- Real update: a build of this branch calling itself v0.59 applied v0.60 from the panel API
  on a scratch instance and ended up byte-identical to the release (`8ae64a5b...`). Nothing
  was written under `<data>/update` or `$HOME/.sigstore`.
- The same build calling itself v0.61 was not offered v0.60, and the API answered "no update
  available".

## Not done

- A provenance refusal seen through the running panel. It talks to `api.github.com` only, and
  there is no rewritten release to point it at; those refusals are covered by the tests.
- arm64 is built and its attestation tested, never run. The hardened systemd unit's staged
  swap is not touched by this change.
- Private repositories and forks with their own workflow file, as above.
- Only an attestation GitHub returns inline is read. One returned as a `bundle_url` alone is
  skipped, which refuses the update.
- Renaming or transferring the repository strands every install: the certificate would name
  the new repository and the binary still asks for the old one. Updating by hand is the fix.
- When several attestations fail, the message carries the last one's reason only.
