# Spice Toolchain

Unified documentation: [spiceframework.dev/toolchain](https://spiceframework.dev/toolchain/).

This repository contains Spice's compile-time developer toolchain: the
compiler, generator, command-line interface, language server, scaffold support,
and official annotation protocol executable. Runtime APIs, public annotation
descriptors, and the annotation SDK live in
[`github.com/spice-framework/spice`](https://github.com/spice-framework/spice).

The boundary is intentionally explicit:

- `github.com/spice-framework/spice` owns public application-facing Go APIs.
- `github.com/spice-framework/toolchain` owns compiler and executable code.
- applications authorize `cmd/spice`, `cmd/spice-annotation-core`, and the
  optional `cmd/spicestyle` schema-two verifier with ordinary Go `tool` directives;
- the official annotation process serves descriptors from the core module, but
  third-party descriptors and tools remain in the same resolved Go module;
- generated application code imports public core packages and never imports the
  compiler or CLI.

## Install in an application

With Go 1.26.5:

```text
go get -tool github.com/spice-framework/toolchain/cmd/spice@<exact-version>
go get -tool github.com/spice-framework/toolchain/cmd/spice-annotation-core@<exact-version>
go get -tool github.com/spice-framework/toolchain/cmd/spicestyle@<exact-version>
```

Then run the complete package-oriented workflow:

```text
go tool github.com/spice-framework/toolchain/cmd/spice verify ./...
go tool github.com/spice-framework/toolchain/cmd/spice generate ./...
go tool github.com/spice-framework/toolchain/cmd/spice build ./...
```

Applications that want class-oriented source organization can enable the
schema-two profile. `spicestyle` and `spice verify --style` both use the same
strict decoder, exact build-selection executor, structural checks, and typed
annotation/provider/module validation:

```text
go tool github.com/spice-framework/toolchain/cmd/spicestyle --config=.spice/style.json ./...
go tool github.com/spice-framework/toolchain/cmd/spice verify --style=.spice/style.json ./...
```

The profile keeps valid Go while enforcing one primary named type per ordinary
production file, initialism-aware filenames, receiver-method and constructor
co-location, approved boundary files, context/error conventions, explicit
managed-interface relationships, and the absence of loose behavior or mutable
globals. `doc.go`, an exact package-main entrypoint, one typed `*_bean.go`
provider, and one typed `*_topic.go` marker remain deliberate Go/Spice
boundaries. The Spice core repository owns the normative
[`CODE_STYLE.md`](https://github.com/spice-framework/spice/blob/0e79bc4f3b294cd0a429598c4921391f2e4d10e2/CODE_STYLE.md)
contract at commit `0e79bc4f3b294cd0a429598c4921391f2e4d10e2`, with reviewed
SHA-256 `09c014e2d7eb93bf2b395e24e4e6ff2466c05d164d4778a11cf7433164bffb76`.
Toolchain does not maintain a second schema-two policy copy.

Schema two is decoded once per verification request. Unknown fields, schema
one, invalid or noncanonical roots, platforms, tags, and build-selection order
fail closed. Every declared selection is loaded independently with ambient
`GOFLAGS`, `GOOS`, `GOARCH`, and `CGO_ENABLED` removed; duplicate physical
diagnostics retain the ordered selection names, and a handwritten file that no
selection reaches is an error. For schema-two verification, configured source
roots are authoritative; trailing CLI package patterns do not narrow away part
of that reviewed universe. Within each selection, the compiler derives every
`@Application` package and compiler-validated `spice_generate` entrypoint, then
validates each application composition independently. Identical configuration
keys, environment names, providers, or routes in unrelated applications do not
collide; duplicates within one composition still fail, and application-semantic
source unreachable from every derived target is an error. Diagnostic
relationships name both the build selections and application targets involved.
Each scoped pass also promotes only transitively imported packages from the
same Go module, so local `@Module` declarations remain visible without loading
unrelated applications. Promoted packages are reloaded as exact roots so their
syntax, file set, Go types, and type information are complete; packages outside
configured source roots still fail the exact source-ownership check. Module and
named-interface identities discovered across declared build selections form a
compiler-derived registry: a dependency that is valid but inactive for one
platform remains known, while nonexistent identities still fail and inactive
modules never create packages or edges in that platform's graph.

Filename suffixes are selection-scoped rather than globally allowlisted. A
primary type may use its canonical filename or the active `GOOS`, `GOARCH`,
`GOOS_GOARCH`, Go platform alias, explicitly constrained `unix` family, or one
whole positively required declared tag. Arbitrary role suffixes and suffixes
from another selection fail closed. An `@Application func main` outside
`main.go` is accepted only when its exact `spice_generate` constraint, generated
package import, and `os.Exit(generated.Main(os.Args[1:]))` body are validated by
the compiler. Generated-root membership and the standard generated header
independently exclude generated code. Boundary, function, and variable
exceptions match exact workspace-relative paths; variable types use full Go
package identity. The typed phase enforces explicit managed scopes, private
managed fields, exact module ownership/dependencies, and protected-or-reviewed-
public route classification. The generated scaffold enables every schema-two
family. LSP clients select the same contract with the `spice.style` setting (for
example `.spice/style.json`); compiler-owned generation tags remain explicit and
cannot reintroduce ambient build state.

Create a profile-shaped application and class-oriented declarations with:

```text
go tool github.com/spice-framework/toolchain/cmd/spice init --module example.com/shop --profile=java-structured
go tool github.com/spice-framework/toolchain/cmd/spice new module orders
go tool github.com/spice-framework/toolchain/cmd/spice new service OrderService --directory internal/orders --package orders
go tool github.com/spice-framework/toolchain/cmd/spice new repository OrderRepository --directory internal/orders --package orders
go tool github.com/spice-framework/toolchain/cmd/spice new controller OrderController --directory internal/orders --package orders
go tool github.com/spice-framework/toolchain/cmd/spice new component PasswordHasher --directory internal/orders --package orders
go tool github.com/spice-framework/toolchain/cmd/spice new enum OrderStatus --directory internal/orders --package orders
```

`spice init` writes both tool declarations and independently pins the public
core and toolchain module versions. Java-structured initialization places the
application boundary in `cmd/<application>/main.go`, an assembly-module
`cmd/<application>/doc.go`, and an initial
`internal/<application>/doc.go` module root. Declaration scaffolds use
deterministic filenames and exact `New<Type>` constructors. Neither command
overwrites source, downloads modules, invokes Go, or initializes version
control. The original `spice new --module ...` application form remains
supported.

## Service method policies

Interface-bound singleton services can declare `@data.Transactional`,
`@security.Authorize`, `@cache.Cacheable`, `@retry.Retryable`, and
`@observability.Observed` on exported methods whose first parameter is exact
`context.Context` and whose final result is `error`. The compiler generates one
direct-call decorator and injects only the explicitly declared `@Implements`
interface. Injecting the raw concrete service is a compile-time error.

Policies have one stable nesting order: observation, authorization, cache,
retry, transaction, then the concrete target call. Cache hits therefore skip
retry and transaction; authorization guards cache access; each retry attempt
owns its own transaction; observation covers the complete logical call. The
generated implementation uses no reflection, runtime proxy, service locator,
or package-global registry. `ApplicationOptions` owns retry, cache,
authorization, and method observers per application instance.

## Spice-native logging

`@observability.Logging` generates an instance-owned
`*logging.Logger`, registers exact compiler-owned module/component scopes, and
makes that logger injectable through the ordinary typed provider graph. An
application `@Bean` returning the exact logger type replaces the generated
fallback. Generated `Application.Logger` and `Application.LoggingController`
accessors expose the selected capability without a registry or global slog
state.

Generated configuration owns `spice.logging.format`, `spice.logging.level`,
`spice.logging.levels`, and `spice.logging.add-source`. Embedded applications
discard output unless `ApplicationOptions.Logging` supplies exactly one writer
or handler; generated commands bind their caller-owned stderr. The deprecated
`*slog.Logger` option remains a one-preview compatibility adapter and conflicts
with the new option when both are set.

Logging adapters are ordered before application observers across lifecycle,
HTTP, authorization, scheduling, async, retry, cache, events, transactions,
and observed methods. `management.Enable(expose=["loggers"],
access="loopback")` additionally generates method-specific GET/POST logger
control routes and is rejected unless logging is enabled.

## Develop

The repository requires exactly Go 1.26.5. Use:

```text
make fast
make check
make benchmark
make verify
```

The verifier is implemented in Go, so its behavior is the same from PowerShell,
Linux, and macOS. The definitive gate checks module and vendor reproducibility,
builds every published tool, tests the compiler/CLI/LSP boundary, rejects stale
monorepository imports, enforces pinned formatting, lint, nil-safety, security,
race, fuzz, and 85% coverage gates, and generates a third-party SDK fixture
twice from zero before compiling and executing it offline.

The definitive gate also enforces the versioned latency, allocation, and memory
ceilings in [`benchmarks/budgets.json`](benchmarks/budgets.json). Five samples
per critical path are reduced to a median to limit scheduler noise. Run
`make benchmark` for that focused contract without waiting for the full gate;
the budgets remain mandatory in `make verify` and therefore in every release.

Before the offline fixture tests, the gate explicitly acquires and verifies the
fixture's declared Go modules. Product analysis, generation, LSP operation, and
the subsequent fixture workflow all run with network lookup disabled.

The third-party fixture under `testdata/` is handwritten. Its generated output
is deliberately untracked and recreated by verification.

## Generator compatibility

The frozen `v0.1.0-preview.4` generator contract is recorded in
[`compatibility/generator.json`](compatibility/generator.json): it
writes ownership schema 6, accepts guarded migrations from schemas 1 through
6, uses Go formatting line 1.26, and fails closed on manual edits, stale owned
files whose hashes changed, unsafe paths, or non-canonical contract drift.
Source-built `go tool` binaries report the same exact generator identity; the
release command rejects any tag that differs from it.

The existing `v0.1.0-preview.1` and `v0.1.0-preview.2` releases remain
immutable history. Preview.1 records schema 5 and `0.1.0-dev`; release run
`31120527225` was cancelled. Preview.2 was published by successful release run
`31403311626` from commit `bab8bcaf`. Preview.3 is an immutable tag-only
attempt at commit `38ddce15`; release run `31501018109` stopped before
attestation and created no release. Preview.4 is the current immutable
published distribution: annotated tag object
`c56a53983f4e36259ac83a016fd80326023a730d` resolves to commit
`35cb1315bb30bc31b82fdd71c99c6313b4b4a923`, and successful release run
[`31522099046`](https://github.com/spice-framework/toolchain/actions/runs/31522099046)
published its exact ten-asset prerelease.

## Release

[`RELEASING.md`](RELEASING.md) defines the current release contract. The
preview.4 candidate first bootstraps only its five committed Go graphs through
the public proxy and checksum database, then runs the complete release gate
offline. Development authority
`73ec26480db3247cd93c8325080058e118b845c9` produces six deterministic
platform archives, canonical release metadata, an SPDX 2.3 SBOM, and
checksums. Independent Toolchain authority
`aeb2b789fedf5b7a45f0d0869043c8568161edad` authenticates the tagged source
and graph, reconstructs every byte, and copies exactly those nine subjects into
a verifier-owned directory. Organization caller authority
`a56c451168aae0f2b3075782156d204d75fb7f69` preserves that policy intersection.
Linux and Windows then execute only the installed verified archive. Protected
`release-attestation` approval mints keyless Sigstore provenance, its source
and workflow identity are authenticated, and distinct `release-publish`
approval is the only path to a ten-asset public prerelease. The caller passes
no secrets and no earlier job has repository write authority.

The retained Ed25519 builder and its eleven-asset workflow describe the
historical preview.2 release boundary. They remain available for verification
and compatibility evidence, but they are not the preview.4 production
authority.

Signed source-only starter releases use a separate trust boundary. The
`cmd/spice-library-release-verify` Go tool authenticates the exact five-file
artifact set with an externally trusted Ed25519 public key, then independently
checks the source archive and SPDX 2.3 document against an exact commit in the
trusted starter checkout. Callers must also provide the expected canonical
HTTPS source URL and Go module path; repository-name coincidence alone is not a
trust decision. It does not import the central development renderer, the
retained starter builder, or this repository's binary-release builder. Starter
workflows authorize an exact verifier version with an ordinary root `go.mod`
`tool` directive and run it in vendor-only offline mode.

Generic `go-module-v1` releases use the separate
`cmd/spice-go-release-verify` boundary. It accepts only dependency-free
`spice@v0.1.0-preview.4`, `spice-agent@v0.1.0-preview.7`,
`spice-agent-provider-openai@v0.1.0-preview.1`,
`spice-agent-tools-coding@v0.1.0-preview.1`, and
`spice-agent-tui@v0.1.0-preview.2`. It rejects starters and distribution
profiles, and independently binds repository, source, module, version, commit,
committed release intent, required module selections, vendor selection,
archive bytes, release metadata, checksums, and SPDX 2.3 contents. It runs only
an archive-materialized copy of the exact Git tree: dependencies are downloaded
into verifier-private caches through `https://proxy.golang.org` and
`sum.golang.org`, the committed vendor tree is compared byte-for-byte and
mode-for-mode with a fresh authenticated regeneration, and the isolated tree
then passes offline vendor-only listing and a `-trimpath` build. Candidate
`go.mod` and `go.sum` changes during authentication fail closed. Public graph
authentication uses a private alternate modfile and sumfile inside the isolated
workspace, so Go may complete checksum evidence without changing those tidy
candidate files. CGO, credentials, private-module exceptions, workspace
selection, and ambient Go configuration are disabled. One validated absolute Go executable and
verifier-private Go path, caches, temporary storage, and disabled telemetry
remain fixed across every command.
Downloaded module-cache entries may be read-only by Go design. The verifier
first attempts ordinary private-workspace removal, repairs only owner access
inside that private tree when required, and retries removal without following
symlinks. A persistent cleanup failure remains a verification error, so an
apparently successful verification cannot silently leak temporary state.
The Agent core preview.7 policy requires authenticated Spice preview.4 and the
published Toolchain preview.2. Provider preview.1, coding-tools preview.1, and
the `spice-agent-coding` preview.4 distribution retain the immutable Spice
foundation `v0.1.0-preview.2` and their exact historical Toolchain, Agent, and
sibling selections. The separately authorized TUI preview.2 policy requires
Spice preview.4 and Toolchain preview.4. Toolchain distribution preview.4
likewise requires Spice preview.4 and authorizes only one `spice` command, six
targets, LICENSE, README, and the two typed CLI identity symbols. Spice
preview.3 is an immutable tag-only failed attempt that produced no
authenticated release; it is rejected as a current foundation selection. This
closed authority independently matches Development commit
`73ec26480db3247cd93c8325080058e118b845c9`; neither repository can expand it
alone.

In the separate Toolchain distribution history, preview.1 release run
`31120527225` was cancelled. Published preview.2 remains
immutable at commit `bab8bcaf` through successful release run `31403311626`.
Preview.3 release run `31501018109` passed candidate validation, deterministic
rendering, independent nine-subject verification, and Ubuntu installed-byte
execution. Windows then rejected the runner's mixed-separator verified
artifact directory before installed-byte execution, so attestation,
provenance authentication, and publication were skipped. Preview.3 remains an
immutable tag-only attempt with no GitHub Release or deployment. Preview.4
annotated tag object `c56a53983f4e36259ac83a016fd80326023a730d`
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
Provider and coding-tools releases and all three Coding distribution sibling
selections remain preview.1; TUI's own policy alone advances to preview.2.
Provider, coding-tools, and distribution policies require
`spice-agent@v0.1.0-preview.4` and reject preview.1, preview.2, and preview.3;
that dependency graph remains unchanged while the Agent module's own closed
policy advances independently to preview.7. Successful release run
`31428824060` published preview.6 as a non-draft prerelease with its
authenticated five-asset module set. Preview.7 annotated tag object
`251bd3b86c6c731cf2b8f20b57430130d31fde7e` resolves to commit
`831fbf259ff3896067a7c6d74d4f402310214805`; unique release run
[`31519742953`](https://github.com/spice-framework/spice-agent/actions/runs/31519742953),
attempt 1, and protected deployments `5855895060` and `5855923346` published
the [immutable five-asset prerelease](https://github.com/spice-framework/spice-agent/releases/tag/v0.1.0-preview.7).
Fresh proxy and SumDB resolution yields module sum
`h1:BQS23GwLBm5BLaRqMB9vYu+0dcEnuP6ooG6tzyjDSjY=` and go.mod sum
`h1:WKNPxU7+jt+aPdL8v1aXovw9D32PwTYq3hE4xPug1YE=`. Preview.7 remains a
distinct identity and does not move, replace, or reuse preview.5 or preview.6.
Before creating an immutable tag, release operators can run
`spice-go-release-verify policy-check` with the proposed repository, canonical
source, module, version, and profile. This bounded, deterministic check reads
only the verifier's compiled policy and performs no Git, filesystem, artifact,
module, or network operation. Operators compare its exact authorization with
the separately reviewed development plan through one tab-separated
`profile`, `repository`, `module`, and `version` line. Toolchain validates the
canonical `source` input before emitting that directly comparable tuple. Full
post-tag verification remains mandatory.
The dependency-free Spice policy omits both `go.sum` and vendor. It rejects
requirements, tools, replacements, excludes, ignores, partial graph metadata,
and every `vendor/` path, then independently proves the selected module and
package graphs and builds with network disabled and `-mod=readonly`.
The command imports neither the development catalog nor its renderer. The
organization workflow must pin the development renderer and this verifier from
separate immutable commits before keyless attestation. A successful invocation
copies exactly the four verified artifact bytes into a required, previously
absent verifier-owned output directory; attestation must never consume the
renderer directory directly.

Binary distributions use the sibling
`cmd/spice-go-distribution-release-verify` boundary. Its closed
`go-distribution-v1` policies authorize the unchanged `spice-agent-coding`
surface and the separate Toolchain preview.4 surface. Each fixes its commands,
six operating-system/architecture targets, committed payloads, required
modules, and typed build-identity symbols. The verifier materializes
the exact tagged Git tree, publicly authenticates and byte-compares regenerated
vendor, performs network-disabled `-trimpath` builds for every target, proves
the exact symbols with `go tool nm`, executes host binaries through
`--version`, and independently reconstructs every archive, metadata document,
SPDX SBOM, and checksum. The renderer output is never the attestation input;
successful verification copies the exact validated artifact allowlist into a
required absent verifier-owned directory.

The Toolchain candidate also owns an installed-byte gate for that verified
allowlist. Set `SPICE_DISTRIBUTION_VERIFIED_ARTIFACT_DIR` to the canonical
absolute verifier-owned output directory. Windows ephemeral runners must also
set `SPICE_DISTRIBUTION_EPHEMERAL_RUNNER=1`; non-Windows runners must leave it
unset. Then run:

```text
make verify-release-artifacts
```

The gate accepts only the preview.4 Toolchain nine-subject set: checksums,
release metadata, SPDX SBOM, and all six Linux/macOS/Windows amd64/arm64
archives. Every archive must contain exactly one `spice` binary plus the
committed LICENSE and README with canonical paths, bytes, and permissions. The
host archive is extracted into private scratch space and its binary is executed
offline; it must report exactly `spice 0.1.0-preview.4 (<40-character-commit>)`.
Source builds report the honest development identity
`spice v0.1.0-preview.4 (development)`. Release builds set the exported
`internal/cli.Version` and `internal/cli.Commit` data symbols directly; mixed,
empty, noncanonical, or malformed linker identities fail closed. This
candidate check consumes independently verified bytes and does not replace
source authentication, independent reconstruction, provenance, or publication
approval.

The separately explicit `make release-acceptance` proof is network-capable by
design and is not part of `make verify`. It clones the central development
signer and starter-oidc at repository-pinned commit IDs, creates a clean
temporary checkout with the canonical HTTPS origin and an exact temporary tag,
signs with a newly generated ephemeral Ed25519 key, and passes those artifacts
to this repository's independent verifier with explicit source and module
identity. Its hosted workflow runs on Linux and Windows. All Go builds remain
vendor-only and offline; network access is limited to the two pinned Git
fetches, and the temporary clones, artifacts, and private key are deleted when
the proof finishes.

## Extraction provenance

The repository retains the filtered history of the compiler/tooling boundary.
Its public-core bridge is pinned to
`github.com/spice-framework/spice@v0.1.0-preview.2.0.20260811041952-0e79bc4f3b29`
(commit `0e79bc4f3b294cd0a429598c4921391f2e4d10e2`). The Apache-2.0 license and
pinned quality-tool versions were carried from the extracted source history,
then the module identities and standalone gate were adapted here. No migration
script or machine-specific replacement is part of the published tree.

## Extraction status

This initial standalone boundary intentionally runs `cmd/spice` through the
handwritten CLI, exactly like `cmd/spice-bootstrap`. The former monorepository's
stale generated self-hosted command was removed rather than published under the
wrong module identity. Production self-hosting remains a follow-up integration
milestone; this repository does not claim it yet. Performance budgets and the
artifact build are guarded and reproducible as documented above.

## License

Apache License 2.0. See [LICENSE](LICENSE).
