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
  optional `cmd/spicestyle` analyzer with ordinary Go `tool` directives;
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
two-layer profile. The standalone analyzer enforces Go structure from a strict
configuration, while `spice verify` adds typed annotation, provider, and module
validation:

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
[`CODE_STYLE.md`](https://github.com/spice-framework/spice/blob/0d4ed59cd3a618011d3c4f493714a3a67070ee84/CODE_STYLE.md)
contract at commit `0d4ed59cd3a618011d3c4f493714a3a67070ee84`, with reviewed
SHA-256 `9beeec406dba8f9a6c288dd83d2bac60955885c7d5811c37518165cf94673f24`.
Toolchain does not maintain a second schema-two policy copy.

Schema two is decoded once by the standalone structural analyzer and typed
compiler phase. Unknown fields, schema one, invalid or noncanonical roots,
platforms, tags, and build-selection order fail closed. A schema-one failure
names the required migration to schema two and `buildSelections`. Enabled
rules are checked against a phase-capability registry; the scaffold therefore
keeps `explicitManagedScopes`, `privateManagedFields`, `moduleOwnership`, and
`routeClassification` off until their typed diagnostic families land. Exact
multi-context execution of every validated build selection remains the next
bounded Toolchain slice; this slice does not claim that loading guarantee yet.
The LSP accepts the profile in its Spice settings and offers source generation
for validated `@Enum` helpers.

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

The next immutable generator candidate is `v0.1.0-preview.2`. Its canonical
contract is [`compatibility/generator.json`](compatibility/generator.json): it
writes ownership schema 6, accepts guarded migrations from schemas 1 through
6, uses Go formatting line 1.26, and fails closed on manual edits, stale owned
files whose hashes changed, unsafe paths, or non-canonical contract drift.
Source-built `go tool` binaries report the same exact generator identity; the
release command rejects any tag that differs from it.

The existing `v0.1.0-preview.1` tag remains immutable but is not the frozen
generator contract: it records schema 5 and `0.1.0-dev`, and its release run
`31120527225` is still waiting at the protected signing boundary. Do not
approve, move, or reuse that tag as a generator-freeze shortcut. Preview.2
must first pass the complete local and hosted candidate gates, then be tagged
and released only under separate authorization.

## Release

[`RELEASING.md`](RELEASING.md) defines the release contract. Production builds
require a clean checkout at the exact canonical SemVer tag, the tag's commit epoch,
Go 1.26.5, and an external Ed25519 signing key. Rehearsals are deliberately
unsigned. Every build uses the committed Git snapshot, the vendor graph, a
scrubbed offline Go environment, and emits deterministic platform archives, a
source archive, an exact SPDX SBOM, checksums, and (for production) a detached
checksum signature. The tag workflow independently authenticates the signed
artifacts against the committed trust anchor, compares a clean Windows rebuild
byte-for-byte, and grants repository write authority only after a separate
protected publication approval. That final job creates a private draft,
downloads and reverifies every byte, and only then publishes it. The trust
anchor and protected environments in
[`RELEASING.md`](RELEASING.md) are mandatory before creating a release tag.

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
`spice@v0.1.0-preview.2`, `spice-agent@v0.1.0-preview.6`, and the other three
explicitly reviewed Spice Agent module
policies, rejects starters and distribution
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
All four Agent module policies and the `spice-agent-coding` distribution policy
require the immutable Spice foundation `v0.1.0-preview.2`; their toolchain,
sibling-module, metadata, binary, and payload selections remain independently
pinned. The distribution's next authorized version is `v0.1.0-preview.4`;
preview.1 is rejected after release run `31333877865`
stopped at the missing candidate `verify-release` target before rendering or
artifact production. Published preview.2 remains immutable. Preview.3 release
run `31345003119` passed validation, rendering, and independent verification,
then failed before attestation because Linux retained a stale preview.2
installed-artifact expectation and Windows rejected a valid mixed-separator
runner path. Preview.4 authorizes only the corrected candidate boundary.
Release run `31349650978` subsequently completed installed-byte execution on
Linux and Windows, keyless attestation, provenance authentication, and
protected publication of the immutable preview.4 prerelease.
Provider,
coding-tools, and TUI releases and all three distribution sibling selections
remain preview.1.
Provider, coding-tools, and distribution policies require
`spice-agent@v0.1.0-preview.4` and reject preview.1, preview.2, and preview.3;
that dependency graph remains unchanged while the Agent module's own release
advanced independently to preview.6. Successful release run `31428824060`
published preview.6 as a non-draft prerelease with its authenticated five-asset
module set. Its distinct immutable identity ensures subsequent Agent source
never moves, replaces, or reuses preview.5 or preview.6.
Before creating an immutable tag, release operators can run
`spice-go-release-verify policy-check` with the proposed repository, canonical
source, module, version, and profile. This bounded, deterministic check reads
only the verifier's compiled policy and performs no Git, filesystem, artifact,
module, or network operation. Operators compare its exact authorization with
the separately reviewed development plan through the deterministic JSON
`profile`, `repository`, `module`, and `version` fields; Toolchain additionally
binds `source`. Full post-tag verification remains mandatory.
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
`go-distribution-v1` policy authorizes only `spice-agent-coding`, its two
commands, six operating-system/architecture targets, committed payloads,
required modules, and typed build-identity symbols. The verifier materializes
the exact tagged Git tree, publicly authenticates and byte-compares regenerated
vendor, performs network-disabled `-trimpath` builds for every target, proves
the exact symbols with `go tool nm`, executes host binaries through
`--version`, and independently reconstructs every archive, metadata document,
SPDX SBOM, and checksum. The renderer output is never the attestation input;
successful verification copies the exact validated artifact allowlist into a
required absent verifier-owned directory.

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
