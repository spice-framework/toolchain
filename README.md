# Spice Toolchain

This repository contains Spice's compile-time developer toolchain: the
compiler, generator, command-line interface, language server, scaffold support,
and official annotation protocol executable. Runtime APIs, public annotation
descriptors, and the annotation SDK live in
[`github.com/spice-framework/spice`](https://github.com/spice-framework/spice).

The boundary is intentionally explicit:

- `github.com/spice-framework/spice` owns public application-facing Go APIs.
- `github.com/spice-framework/toolchain` owns compiler and executable code.
- applications authorize both `cmd/spice` and `cmd/spice-annotation-core` with
  ordinary Go `tool` directives;
- the official annotation process serves descriptors from the core module, but
  third-party descriptors and tools remain in the same resolved Go module;
- generated application code imports public core packages and never imports the
  compiler or CLI.

## Install in an application

With Go 1.26.5:

```text
go get -tool github.com/spice-framework/toolchain/cmd/spice@<exact-version>
go get -tool github.com/spice-framework/toolchain/cmd/spice-annotation-core@<exact-version>
```

Then run the complete package-oriented workflow:

```text
go tool github.com/spice-framework/toolchain/cmd/spice verify ./...
go tool github.com/spice-framework/toolchain/cmd/spice generate ./...
go tool github.com/spice-framework/toolchain/cmd/spice build ./...
```

`spice new` writes both tool declarations and independently pins the public core
and toolchain module versions. It never downloads modules implicitly.

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
`github.com/spice-framework/spice@v0.1.0-preview.1.0.20260807010518-0cacff461fbb`
(commit `0cacff461fbb66a21b1f5c02dca61f81e2d7509a`). The Apache-2.0 license and
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
