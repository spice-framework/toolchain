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
  anchor. Its DER SHA-256 fingerprint is
  `9be4a0a3d312e48ccc1c17136510e7658c5d1fcda8f95ab2e938b6ffb0d97272`.
  A generated `checksums.txt.pem` is not a trust anchor by itself.
- The protected `release-signing` environment permits only release tags,
  requires the repository owner to approve, and contains the
  `SPICE_RELEASE_SIGNING_KEY_FILE_B64` secret. The
  secret is the base64 encoding of the complete PKCS#8 PEM or supported raw-key
  file, not a path.
- The protected `release-publish` environment has the same reviewer and tag
  restrictions. It contains no signing material and provides a distinct
  approval before the only job with repository write authority creates and
  publishes the verified draft.
- An enforced tag ruleset restricts creation of `v*` tags to release managers
  and forbids tag updates and deletion.
- The default branch rejects deletion, non-fast-forward updates, and nonlinear
  history. The repository's direct-main contract requires the exact local
  `make verify`, a fetch guard, and a clean push; hosted verification is the
  post-push durability mirror.
- Every third-party workflow action is pinned to a full commit SHA. Actionlint
  and the repository workflow-invariant tests reject authority drift.

The organization currently has one maintainer, so GitHub cannot require a
different human reviewer without making releases impossible. Environment
self-review is therefore enabled deliberately. The two sequential approvals,
credential separation, immutable tag, credential-free rebuild, and repeated
artifact verification remain mandatory. Add a distinct required reviewer and
prevent self-review as soon as a second release maintainer is available.

The signing key is generated outside the repository and transferred directly
to the protected GitHub environment. This solo-maintainer setup deliberately
retains no plaintext local copy; loss of the hosted secret requires a reviewed
trust-anchor rotation before another release tag is created. Never store the
private key or its decoded bytes in the repository, workflow artifacts, logs,
or release assets.

## Automated production release

1. Run `make verify` on exactly Go 1.26.5 and commit the green tree. This gate
   includes the reviewed toolchain performance budgets in
   `benchmarks/budgets.json`; do not raise a ceiling without measured evidence
   and a recorded rationale.
2. Confirm the commit is on `origin/main`, CI is green, the working tree is
   clean, and the intended version is a canonical version such as `v0.1.0` or
   `v0.1.0-preview.1`. Build metadata is not accepted.
3. Have an authorized release manager create and push the immutable `v*` tag.
   Do not manually create a GitHub Release.
4. Approve `release-signing` only after checking the tag, commit, workflow, and
   trust-anchor history.
5. Inspect the retained signature, reproducibility, and verification evidence.
6. Approve `release-publish` only after the signed build and independent
   Windows rebuild have passed byte-for-byte verification.

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
5. Wait at the protected publication environment. No earlier job has repository
   write permission.
6. After approval, create or resume only a matching private draft, reject
   unknown assets, upload exactly the verified eleven files, download them,
   independently verify the signature, source, archives, binaries, module
   graph, SBOM, and checksums again, and compare every downloaded byte with the
   originally verified workflow artifact.
7. Retain pre-publication evidence for 90 days and make the matching draft
   public only after every in-job recheck succeeds.

Signed and unsigned workflow intermediates are retained for 14 days. A failed
job never publishes a release. A failure after draft creation deliberately
leaves the release private for inspection; a rerun may replace only expected
assets on a matching draft and still repeats every verification gate.

For local production diagnosis, the guarded builder remains available:

```text
go run ./cmd/spice-release -version v0.1.0 -signing-key ../private/spice-release.key -output dist-v0.1.0
```

Production validation rejects build metadata, a tag that does not resolve to
`HEAD`, a source epoch that differs from the `HEAD` commit timestamp,
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

## Starter-library verification

`cmd/spice-library-release-verify` is the independent verifier for signed
source-only Spice starter releases. It accepts an untrusted artifact directory,
an exact trusted starter commit and repository name, and a separately
provisioned Ed25519 public-key PEM. It requires exactly the source archive,
SPDX 2.3 SBOM, checksums, detached signature, and emitted public key;
authenticates the checksum bytes before reading their claims; and verifies
archive paths, modes, symlinks, timestamps, content, module sums, vendor
selection, compatibility metadata, and renderer schema against trusted Git
objects.

This command does not render artifacts and has no dependency on
`internal/release`, the central development renderer, or retained starter
builders. A key shipped beside its own signature is never accepted as the trust
anchor: callers must pass the independently provisioned public-key file with
`-trusted-public-key`. The canonical source and module are separate trusted
inputs, so a repository with the same basename on another host cannot satisfy
the contract:

```text
go tool github.com/spice-framework/toolchain/cmd/spice-library-release-verify \
  -artifacts dist -root . -repository starter-oidc \
  -source https://github.com/spice-framework/starter-oidc \
  -module github.com/spice-framework/starter-oidc \
  -version v1.2.3 -commit <exact-object-id> \
  -trusted-public-key <independently-provisioned-key.pem>
```

Renderer/v1 limits compatibility metadata to 64 KiB, the selected module graph
and committed `go.sum` to 16 MiB each, the emitted SPDX document to 1 MiB, each
expanded source entry to 128 MiB, and the complete expanded source archive to
256 MiB. The verifier duplicates these constants deliberately instead of
importing producer code. Changing a limit requires a new renderer contract and
cross-producer acceptance proof.

Renderer/v1 source paths and symbolic-link targets use printable ASCII bytes
only (`0x20` through `0x7e`). This deliberately prevents Unicode-normalization
aliases and invalid UTF-8 from producing different extraction results across
Linux, macOS, and Windows. File contents remain unrestricted. The verifier also
rejects ASCII case-folded path collisions independently of the producer.

## Cross-producer acceptance

The network-capable cross-producer proof is deliberately separate from the
offline, deterministic `make verify` gate. Run it explicitly with:

```text
make release-acceptance
```

The repository-owned harness pins the central development producer to commit
`afcab67bcb1a6d2893335242df5d76d25afc4d98` and starter-oidc to commit
`24ae4132e4782b8c0957c5d44b85cfcd845a168e`. It fetches only those objects,
constructs a clean temporary starter checkout whose origin is
`https://github.com/spice-framework/starter-oidc.git`, creates the exact
temporary `v1.2.3` tag, and invokes the real central production planner and
signer. A new Ed25519 PKCS#8 private key and matching PKIX public key are
created outside both repositories for that invocation only.

The signed output is then handed to the independent verifier in this
repository with explicit canonical source, module, repository, version,
commit, and trust-anchor inputs. The verifier does not import or build against
the producer. There is no retained signed-artifact fixture, producer-built
verifier, or long-lived acceptance private key. Temporary source, key, and
artifact material is removed on success or failure.

`.github/workflows/library-release-acceptance.yml` runs this exact proof on
Linux and Windows. Go compilation in every checkout is vendor-only with
`GOPROXY=off`; the only network operation is fetching the two exact Git commit
objects from their canonical repositories.
