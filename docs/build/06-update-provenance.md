# 06 Update provenance

Status: built 10/11/2026 on `feat/update-provenance` for #90. Not part of the MCP scaffold.
Whether it ships in v0.61 is Pablo's call: it adds a dependency and memory, see Cost.

## Goal

The panel's self-updater installs a binary only when GitHub holds build provenance for those
exact bytes and it verifies. `checksums.txt` lives in the release beside the binary, so it
catches a broken download and nothing else. On failure the update is refused and the binary
deleted. `WINDLASS_NO_SELF_UPDATE` stays the way out.

## Fills

- `internal/update/provenance.go`: `verifyProvenance` fetches the Sigstore bundles for the
  binary's SHA-256 from GitHub's attestations API and verifies them with `sigstore-go`.
  `checkBuild` then checks the statement type, source repository, tag and runner.
- `internal/update/update.go`: `fetchVerified` holds the download, the checksum and the
  provenance check, and deletes the file when any of them fails. `Apply` only swaps.
- `internal/update/testdata/`: the real v0.60 linux/amd64 attestation and a snapshot of
  Sigstore's trusted root, so the tests run offline.
- `Dockerfile`: Go 1.27, which `sigstore-go` v1.4.0 requires.
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

## Decisions

Settled during the build, flagged for Pablo:

- **`sigstore-go`, not our own verifier.** Doing this by hand means writing certificate
  transparency, Rekor and DSSE verification ourselves, in the one code path that replaces a
  binary running as root. `gh attestation verify` uses the same library.
- **Keys fetched at update time.** Sigstore's trusted root comes over TUF from
  `tuf-repo-cdn.sigstore.dev`, starting from the TUF root compiled in, with no cache on disk.
  A snapshot baked into a release would go stale when Sigstore rotates a key and block every
  update from that version.
- **The tag is pinned.** A genuine v0.60 binary published as v0.61 is refused, so a rewritten
  release cannot roll servers back to a version with a known hole.
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

## Checks

- `go test ./internal/update/`: the real v0.60 attestation passes; other bytes, another tag,
  another repository, a statement edited after signing, a missing log entry, no attestation
  and no Sigstore keys are each refused; a refused binary is not left on disk.
- Removing the provenance call, or the tag from both places it is checked, fails the tests.
- Live, 10/11/2026: the v0.60 binary verified against GitHub and Sigstore in 1.0 s; the same
  binary with one byte appended was refused; the same binary as v0.61 was refused.
- Real update: a build of this branch calling itself v0.59 applied v0.60 from the panel API
  on a scratch instance and ended up byte-identical to the release (`8ae64a5b...`).

## Not done

- A refusal seen through the running panel. It talks to `api.github.com` only, and there is
  no rewritten release to point it at; the refusals are covered by the tests above.
- arm64, and the hardened systemd unit's staged swap, which this change does not touch.
- Private repositories and forks with their own workflow file, as above.
