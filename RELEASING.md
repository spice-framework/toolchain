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

## Production release prerequisites

The tag-triggered workflow is fail-closed. It is not authorized for a first
production release until all of these repository controls exist:

- `security/release/ed25519-public.pem` contains the reviewed Ed25519 trust
  anchor. A generated `checksums.txt.pem` is not a trust anchor by itself.
- The protected `release-signing` environment permits only release tags,
  requires an independent reviewer, prevents self-review and administrator
  bypass, and contains the `SPICE_RELEASE_SIGNING_KEY_FILE_B64` secret. The
  secret is the base64 encoding of the complete PKCS#8 PEM or supported raw-key
  file, not a path.
- The protected `release-publish` environment has the same reviewer and tag
  restrictions. It contains no signing material and provides the distinct
  approval that makes a verified draft public.
- An enforced tag ruleset restricts creation of `v*` tags to release managers
  and forbids tag updates and deletion.
- The default branch requires the complete platform verification matrix and
  protects this workflow, the builder, verifier, trust anchor, and this
  document through code-owner review.
- GitHub-owned Actions are allowlisted and full commit-SHA pinning is required.

The signing key must be generated and backed up outside GitHub. Rotating it
requires a reviewed trust-anchor change and a coordinated environment-secret
change before a release tag is created. Never store the private key or its
decoded bytes in the repository, workflow artifacts, logs, or release assets.

## Automated production release

1. Run `make verify` on exactly Go 1.26.5 and commit the green tree.
2. Confirm the commit is on `origin/main`, CI is green, the working tree is
   clean, and the intended version is a canonical stable version such as
   `v0.1.0`.
3. Have an authorized release manager create and push the immutable `v*` tag.
   Do not manually create a GitHub Release.
4. Approve `release-signing` only after checking the tag, commit, workflow, and
   trust-anchor history.
5. Inspect the private draft and the retained verification evidence.
6. Approve `release-publish` only after the downloaded draft was independently
   reverified.

The workflow then performs this chain:

1. Resolve the tag to the checkout's exact commit, require that commit to be an
   ancestor of `origin/main`, derive its source epoch, run `make verify`, and
   prove the gate did not modify the checkout.
2. Build all six signed archives on a protected Ubuntu runner without module or
   workspace resolution. The signing secret is scoped to this step and its
   decoded temporary file is removed before artifact upload.
3. Independently rebuild the same tree without signing material on Windows.
4. Verify the signed release with `cmd/spice-release-verify` and the committed
   trust anchor, require the exact artifact set, and compare every unsigned
   artifact byte-for-byte between the two hosts.
5. Create or resume only a matching private draft, reject unknown assets, and
   upload exactly the verified eleven files.
6. Download the draft, independently verify its signature, source, archives,
   binaries, module graph, SBOM, and checksums again, and compare all downloaded
   bytes with the originally verified workflow artifact.
7. Retain build evidence for 90 days. After the separate publish approval, make
   one fresh byte comparison and only then change the draft to public.

Signed and unsigned workflow intermediates are retained for 14 days. A failed
job never publishes a release. A failure after draft creation deliberately
leaves the release private for inspection; a rerun may replace only expected
assets on a matching draft and still repeats every verification gate.

For local production diagnosis, the guarded builder remains available:

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

The committed trust anchor and protected GitHub environments remain hard
blockers even when a local signed build succeeds. Never create a tag merely to
test the production workflow; use the unsigned rehearsal instead.
