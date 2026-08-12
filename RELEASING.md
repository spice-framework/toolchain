# Releasing the Spice toolchain

The current Toolchain release uses the organization-owned, keyless
`go-distribution-v1` workflow. Candidate bootstrap is the only network-capable
phase: it seeds private copies of the root, tools, actionlint,
annotationfixture, and annotationapp module graphs through only the public Go
proxy and checksum database. Candidate validation, rendering, independent
verification, installed-byte execution, attestation authentication, and
publication then operate on immutable or verifier-owned bytes.

## Rehearsal

Before creating a tag, use the same boundaries as production:

```text
make tools-bootstrap
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOWORK=off make verify-release
```

In disposable clean checkouts, use Development commit
`87b5d8c3d34ea61c4f293614f364c54d097db469` to render the exact Toolchain
preview.5 nine-subject set and Toolchain verifier commit
`a3df19e40b86686654ab86e521730686f392c50f` to authenticate and reconstruct it.
Run this candidate's `verify-release-artifacts` against only the verifier-owned
output on Linux and Windows. A rehearsal uses a local annotated tag only and
must never push that tag or create a release.

## Production release prerequisites

The tag-triggered workflow is authorized only when all of these controls are
present and independently checked:

- `spice-release.json`, `compatibility/generator.json`, the source generator
  identity, CLI identity, root module, both annotation fixture modules, and
  vendor metadata all name the exact preview.5/Spice preview.4 candidate.
- `make tools-bootstrap` leaves every repository byte and mode unchanged, and
  `make verify-release` succeeds with proxy, checksum lookup, workspace mode,
  and toolchain download disabled.
- `.github/workflows/release.yml` is the no-secrets caller pinned in both
  locations to organization authority
  `d84b2cbce217e2d259ee81727fb98b0c2db1656e`.
- The exact commit is clean, on `origin/main`, and its hosted Verify,
  Documentation, and cross-producer workflows are terminal green.
- Protected `release-attestation` and `release-publish` environments accept
  only release tags and require explicit approval. Neither stores a signing
  key. The enforced tag and release rules forbid tag movement, tag deletion,
  and mutable release assets.
- The reusable workflow authorities remain independently pinned to Development
  `87b5d8c3d34ea61c4f293614f364c54d097db469` and Toolchain
  `a3df19e40b86686654ab86e521730686f392c50f`. No caller edit may expand their
  separately reviewed policy intersection.

## Automated production release

1. Run the fresh-cache bootstrap, offline release gate, `make fast`,
   `make check`, and exact-tree `make verify` on Go 1.26.5. Commit, fetch-guard,
   push, and require the exact hosted workflows to succeed.
2. Complete the disposable clean-clone renderer, independent-verifier, and
   Linux/Windows installed-byte rehearsal without modifying the candidate.
3. After distinct Development and Toolchain authorities authorize preview.5
   and an independent pre-tag audit passes, create the annotated
   `v0.1.0-preview.5` tag with message `Spice Toolchain v0.1.0-preview.5`,
   verify its object and peeled commit locally, and push only that tag. Never
   create the GitHub Release manually and never rerun a failed immutable-tag
   workflow.
4. Confirm candidate validation, deterministic rendering, independent
   reconstruction, and both installed-byte execution jobs succeed. Linux uses
   an unset `SPICE_DISTRIBUTION_EPHEMERAL_RUNNER`; Windows uses exact value `1`.
   Both consume `SPICE_DISTRIBUTION_VERIFIED_ARTIFACT_DIR`.
5. Approve only the waiting `release-attestation` deployment for the exact run,
   tag, commit, jobs, and verified artifact set. Require keyless provenance and
   its source/workflow authentication to succeed.
6. Approve only the subsequent `release-publish` deployment for that same run.
   Require the publish job and complete workflow to finish successfully, then
   independently download and authenticate all release assets.

The workflow then performs this chain:

1. Validate the exact tag, commit, release intent, candidate-owned bootstrap,
   and offline release target without modifying the checkout.
2. Render the six Linux/macOS/Windows amd64/arm64 archives plus canonical
   release JSON, SPDX 2.3 SBOM, and checksums from the separately pinned
   Development authority.
3. Use the separately pinned Toolchain authority to authenticate the public
   module graph, regenerate vendor privately, rebuild and reconstruct every
   subject, and copy exactly nine verified bytes into a new verifier-owned
   directory.
4. On Linux and Windows, download only that verified directory, revalidate its
   exact membership, and execute the native installed binary offline.
5. Wait at `release-attestation`; after approval, attest all nine subjects with
   GitHub OIDC and Sigstore, then authenticate the bundle against the exact
   tag, source repository, caller workflow, and reusable workflow.
6. Wait at `release-publish`; after approval, publish the nine verified subjects
   plus `provenance.sigstore.json` as an immutable non-draft prerelease and
   recheck every downloaded byte and identity.

No failure before publication creates a public release. A failed immutable tag
is historical evidence and is never moved, deleted, reused, or rerun.

## Historical Ed25519 release boundary

Preview.2 was published by successful release run `31403311626` at commit
`bab8bcaf`. Its retained local `cmd/spice-release` builder, committed Ed25519
trust anchor, independent Windows rebuild, detached signature, public key,
source archive, and eleven-asset verification path describe that immutable
historical release. The preview.1 run `31120527225` was cancelled. The retained
legacy `release-signing` environment stores zero secrets. Its removed
private-key secret is not an authority for preview.5 and must not be restored
or substituted into the keyless caller.

The historical builder remains useful for diagnosing and authenticating those
releases, but a local signed build never authorizes a new tag or current
publication. The current generator contract writes ownership schema 6 and
retains the guarded schema-5 migration required by downstream repositories.

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

## Generic Go-module verification

`cmd/spice-go-release-verify` is the independent pre-attestation verifier for
the closed `go-module-v1` profile. The organization workflow supplies every
trusted identity explicitly:

Before creating an immutable tag, compare the separately reviewed development
plan with the independent compiled authorization:

```text
spice-go-release-verify policy-check \
  --repository=spice \
  --source=https://github.com/spice-framework/spice \
  --module=github.com/spice-framework/spice \
  --version=v0.1.0-preview.4 \
  --profile=go-module-v1
```

The command prints the exact four-field tab-separated identity emitted by
Development. `source` remains a required Toolchain trust input and is validated
before the comparable tuple is emitted:

```text
go-module-v1	spice	github.com/spice-framework/spice	v0.1.0-preview.4
```

The separately authorized Toolchain preview.6 distribution identity is:

```text
go-distribution-v1	toolchain	github.com/spice-framework/toolchain	v0.1.0-preview.6
```

Including its terminal LF, this is exactly 83 bytes with SHA-256
`fde8f5c596008bb859a9776650849fbc19562cd7f2d822a05596a0e3e26b5b1d`.

It performs no Git,
filesystem, artifact, module, or network operation. Invalid, missing,
oversized, non-UTF-8, control-bearing, unknown, or stale inputs fail closed
with bounded diagnostics. This is only a pre-tag policy comparison; it does not
validate a candidate commit, dependency graph, artifact, or tag and never
replaces the full verifier below.

```text
spice-go-release-verify \
  -artifacts=<directory> \
  -verified-output=<required-absent-directory> \
  -root=<exact-clean-candidate> \
  -repository=<catalog-name> \
  -source=https://github.com/spice-framework/<repository> \
  -module=github.com/spice-framework/<repository> \
  -version=<catalog-version> \
  -commit=<exact-object-id> \
  -profile=go-module-v1
```

`-artifacts` is untrusted renderer output. `-verified-output` must not already
exist; after every verification and final input re-list succeeds, the verifier
atomically claims that absent directory name without replacement, writes only
the four policy-named bytes with safe modes, and removes any incomplete output
before reporting failure. The
organization workflow uploads, attests, and publishes only that verifier-owned
directory.

The verifier carries its own reviewed allowlist for dependency-free
`spice@v0.1.0-preview.4`, `spice-agent@v0.1.0-preview.7`,
`spice-agent-provider-openai@v0.1.0-preview.1`,
`spice-agent-tools-coding@v0.1.0-preview.1`, and
`spice-agent-tui@v0.1.0-preview.2`, including their required module identities.
A required
tool-only module may retain Go's `// indirect` marker; its canonical `require`
entry remains inspectable beside the `tool` directive. This deliberately
duplicates development catalog policy so one repository cannot
expand release authority by itself. Any repository, version, dependency, or
profile change requires separately reviewed development and toolchain commits.
The Agent core preview.7 policy requires authenticated Spice preview.4 and the
published Toolchain preview.2. Provider preview.1, coding-tools preview.1, and
the `spice-agent-coding` preview.4 distribution retain
`github.com/spice-framework/spice@v0.1.0-preview.2` and their exact historical
Toolchain, Agent, and sibling selections. TUI preview.2 instead requires Spice
preview.4 and published Toolchain preview.4. The independent Toolchain
distribution preview.6 policy likewise requires Spice preview.4 and authorizes
exactly one `spice` command, six targets, LICENSE, README, and the Version and
Commit identity symbols. TUI remains pinned to Toolchain preview.4, and every
other release version and dependency selection remains unchanged.
Spice preview.3 is an immutable tag-only attempt whose release run failed
candidate bootstrap before rendering, verification, attestation, or
deployment; no authenticated preview.3 foundation release exists for either
downstream policy. The complete closed authority independently matches
Development commit `7c847540f9a9c10b38d5fb43159d406b50a0eedf`; neither side
can expand release authority by itself.
Provider, coding-tools, and Coding distribution policies require the recovered
`github.com/spice-framework/spice-agent@v0.1.0-preview.4`; preview.1,
preview.2, and preview.3 are rejected for that dependency. Provider,
coding-tools, and the Coding distribution's provider, coding-tools, and TUI
sibling selections remain preview.1. The `spice-agent-coding` distribution's
authorized version is
`v0.1.0-preview.4`; release run `31349650978` completed the Linux and Windows
installed-byte gates, keyless attestation, provenance authentication, and
protected publication. Published preview.2 remains immutable. The Agent
module's own preview.7 policy does not silently repin provider, coding-tools,
or distribution dependencies, which remain on the exact released preview.4
graph. Agent preview.6 was published by successful release run `31428824060`
with its authenticated five-asset module set. Agent preview.7 annotated tag
object `251bd3b86c6c731cf2b8f20b57430130d31fde7e` resolves to commit
`831fbf259ff3896067a7c6d74d4f402310214805`. Unique release run
[`31519742953`](https://github.com/spice-framework/spice-agent/actions/runs/31519742953),
attempt 1, and protected deployments `5855895060` and `5855923346` published
the [immutable five-asset prerelease](https://github.com/spice-framework/spice-agent/releases/tag/v0.1.0-preview.7).
Fresh proxy and SumDB resolution yields module sum
`h1:BQS23GwLBm5BLaRqMB9vYu+0dcEnuP6ooG6tzyjDSjY=` and go.mod sum
`h1:WKNPxU7+jt+aPdL8v1aXovw9D32PwTYq3hE4xPug1YE=`. Preview.7 remains a
separate identity and cannot move or reuse preview.5 or preview.6.
Every toolchain,
metadata, binary, payload, and target selection remains separately reviewed
and unchanged.

The immutable distribution preview.1 release run `31120527225` was cancelled.
Preview.2 was published from commit `bab8bcaf` by successful release run
`31403311626`. Preview.3 release run `31501018109` passed candidate validation,
deterministic rendering and its reproducibility recheck, independent
nine-subject verification, and Ubuntu installed-byte execution. Windows then
rejected `D:\a\_temp/go-distribution-release-verified` as noncanonical before
installed-byte execution. Attestation, provenance authentication, and publish
were skipped, no protected deployment or GitHub Release was created, and the
preview.3 tag remains an immutable tag-only attempt at commit `38ddce15`.
Preview.4 annotated tag object `c56a53983f4e36259ac83a016fd80326023a730d`
resolves to commit `35cb1315bb30bc31b82fdd71c99c6313b4b4a923`.
Unique release run
[`31522099046`](https://github.com/spice-framework/toolchain/actions/runs/31522099046),
attempt 1, completed candidate validation, deterministic rendering and its
reproducibility recheck, independent verification, Ubuntu and Windows
installed-byte execution, keyless attestation, provenance authentication, and
protected publication. Attestation deployment `5856387461` and publish
deployment `5856422883` both succeeded. The resulting
[immutable ten-asset prerelease](https://github.com/spice-framework/toolchain/releases/tag/v0.1.0-preview.4)
has module sum `h1:mpHAsOdPSUQTSa2GE891VJg5bXmzML0T2N9c5QU4yJg=` and
go.mod sum `h1:nezzFkAq9TDdavVL5sYJm2nOKNWAu1p9VTz3XFihgUg=` from fresh
public proxy and SumDB resolution.
Preview.5 annotated tag object `50498dd91946ec9c049716c34807b4456f5abecd`
resolves to candidate commit `3ed984b56faed8972ed9964c672b7fc2d42a5150`.
Unique release run
[`31557699706`](https://github.com/spice-framework/toolchain/actions/runs/31557699706),
attempt 1, and protected deployments `5862354828` and `5862373810` published
the [immutable ten-asset prerelease](https://github.com/spice-framework/toolchain/releases/tag/v0.1.0-preview.5).
Fresh public proxy and SumDB resolution yields module sum
`h1:FhCM7xedN+CJkIMLuUPjh709+LB54G802daP+Ko57/c=` and go.mod sum
`h1:nezzFkAq9TDdavVL5sYJm2nOKNWAu1p9VTz3XFihgUg=`. Prior tags must never be
moved, reused, or rerun.

Preview.6 is a distinct pre-tag policy identity for the reviewed product line
through core commit `2fd6e6bdd4f7cb8587a8836ab6a180d372025b5f`. This
independent authorization changes only Toolchain's own distribution version
from preview.5 to preview.6. It does not edit candidate-owned version or
compatibility files, repin TUI,
change the no-secrets caller or reusable workflow pins, create a tag, approve
an environment, attest bytes, or publish assets.

The Spice foundation policy is the sole zero-required-module policy. It may
omit both `go.sum` and `vendor/modules.txt`, but never only one. Omission is
accepted only when `go.mod` has no requirements, tools, replacements,
excludes, or ignores; the Git tree has no `vendor/` path; and isolated,
network-disabled `go list -mod=readonly -m all`, package dependency listing,
and `go build -mod=readonly -trimpath ./...` prove that only the main module is
selected. Empty or stale graph files are not treated as equivalent evidence;
the dependency-free canonical form omits them. Any later dependency requires
an explicit non-empty policy and the authenticated vendor path.

Verification requires a clean checkout whose exact tag and `HEAD` resolve to
the supplied commit and whose origin matches the canonical source. It reads
control data from 100644 Git blobs, rejects replacements, checks Go 1.26.0 and
toolchain Go 1.26.5 declarations, requires the exact reviewed
Spice/toolchain/agent versions, and extracts the already byte-verified source
archive into a private workspace. It authenticates the complete selected graph
in fresh module and build caches through only the public Go proxy and checksum
database using a private alternate modfile and sumfile, verifies the cache,
regenerates vendor, and compares every committed
vendor byte and executable mode. Any `go.mod` or `go.sum` mutation fails. It
then runs `go list` and `go build -trimpath` against that isolated tree with
vendor-only network-disabled settings, CGO disabled, credentials removed, and
ambient Go configuration removed. One absolute, resolved Go executable is
bound before verification; private `GOPATH`, module/build caches, temporary
storage, and disabled Go telemetry prevent host-state mutation. The verifier
never builds in the caller worktree and rechecks source identity after all
work. Portable source paths, case collisions, any committed source-tree or
artifact symlink,
missing files, extras, oversized files, checksum drift,
noncanonical JSON, archive differences, metadata differences, and SBOM
differences all fail closed.
Go's downloaded module cache intentionally contains read-only files and
directories. Verifier cleanup retries after restoring owner write/search
access inside its private workspace, does not follow symlinks, and still fails
the release operation if the workspace cannot be removed.

The output is not cryptographically trusted merely because this command
passes. The separately pinned organization workflow next creates keyless
Sigstore provenance over the verified bytes and verifies that bundle against
the exact caller source and reusable-workflow identity before publication.

## Generic Go-distribution verification

`cmd/spice-go-distribution-release-verify` is the independent
pre-attestation verifier for the closed `go-distribution-v1` profile:

```text
spice-go-distribution-release-verify \
  -artifacts=<directory> \
  -verified-output=<required-absent-directory> \
  -root=<exact-clean-candidate> \
  -repository=spice-agent-coding \
  -source=https://github.com/spice-framework/spice-agent-coding \
  -module=github.com/spice-framework/spice-agent-coding \
  -version=v0.1.0-preview.4 \
  -commit=<exact-object-id> \
  -profile=go-distribution-v1
```

The Coding policy retains its exact module selections, two command packages,
six Linux/macOS/Windows amd64/arm64 targets, seven committed payloads, and two
typed identity symbols. The separate Toolchain preview.6 policy authorizes one
`spice` command, the same six targets, LICENSE and README, Spice preview.4 as
its only dependency, and the CLI Version and Commit symbols. It authenticates
the source and module graph exactly as the Go-module verifier does, regenerates
vendor from the public proxy and checksum database in private caches, and then
disables network access. Every binary is independently rebuilt with CGO disabled,
`-trimpath`, no VCS metadata, an empty build ID, and only the policy-owned
version/commit linker assignments. Exact data symbols are proved with
`go tool nm`; host binaries must return the canonical `--version` line.

The verifier independently reconstructs the deterministic tar.gz and zip
archives from those binaries and exact committed payload bytes, as well as the
canonical release metadata, SPDX 2.3 SBOM, and checksums. Missing, extra,
oversized, changed, non-regular, or noncanonical artifacts fail. Source and
artifact identity are rechecked after all builds. Only the bytes copied into a
newly claimed verifier-owned output directory may proceed to keyless
attestation and publication. The implementation imports neither the
development catalog nor `internal/distributionrelease` and never executes the
renderer.

After independent verification has copied the exact Toolchain preview.5
allowlist, the candidate repository verifies its own installed-byte behavior:

```text
SPICE_DISTRIBUTION_VERIFIED_ARTIFACT_DIR=<canonical-absolute-verified-output> \
  make verify-release-artifacts
```

This offline target revalidates exact nine-subject membership, checksums,
canonical release metadata and SPDX identity, all six archive layouts, the
single `spice` binary, LICENSE, README, safe paths, and permissions. It extracts
only the native archive into private scratch space and requires the installed
binary to emit exactly `spice 0.1.0-preview.5 (<exact-commit>)`. The public CLI
identity comes from the directly linker-settable `internal/cli.Version` and
`internal/cli.Commit` variables. Unlinked source builds use the paired honest
development defaults; empty, mixed-development/release, or malformed linker
values are errors. The target neither authenticates source nor creates an
attestation, and it must consume the independently verified directory rather
than renderer output. Windows ephemeral runners must additionally set
`SPICE_DISTRIBUTION_EPHEMERAL_RUNNER=1`; non-Windows runners must leave that
acknowledgement unset.

This installed-byte target remains bound to the published preview.5 candidate
identity until a separate bounded candidate-version change advances those
repository-owned files. Preview.6 policy authorization alone does not alter
that executable contract.

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
