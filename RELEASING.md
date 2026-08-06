# Releasing the Spice toolchain

The release builder consumes one immutable Git `HEAD` snapshot. It does not
package mutable working-tree files and it never downloads modules.

## Rehearsal

Use a rehearsal to inspect deterministic artifacts before creating a tag:

```text
go run ./cmd/spice-release -rehearsal -version v0.1.0-rc.1 -output dist-rehearsal
```

A rehearsal must not receive a signing key. Its checksum file is intentionally
unsigned so it cannot be confused with a production release.

## Production release

1. Run `make verify` on exactly Go 1.26.5 and commit the green tree.
2. Create the canonical stable tag, such as `v0.1.0`, on that commit.
3. Keep the checkout completely clean, including untracked files.
4. Store the Ed25519 PKCS#8 PEM or base64 key outside the repository.
5. Build with the tag's commit timestamp (the default):

```text
go run ./cmd/spice-release -version v0.1.0 -signing-key ../private/spice-release.key -output dist-v0.1.0
```

Production validation rejects prerelease/build metadata, a tag that does not
resolve to `HEAD`, a source epoch that differs from the `HEAD` commit timestamp,
an unsigned build, a dirty checkout, a non-Go-1.26.5 toolchain, module
replacements, and any difference between `go.mod` and `vendor/modules.txt`.

The output contains reproducible archives for every supported target, a source
archive, an SPDX 2.3 SBOM, `checksums.txt`, its raw Ed25519 signature, and the
corresponding public key. The Go build runs with `-mod=vendor`, `-trimpath`, CGO
disabled, workspace and network resolution disabled, and ambient Go build
configuration removed.

Do not push the release tag or publish a GitHub Release until repository CI and
local artifact inspection are green. Independent downloaded-artifact
verification and automated draft publication remain required before the first
public release; the repository does not claim that workflow yet.
