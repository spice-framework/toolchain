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
make verify
```

The verifier is implemented in Go, so its behavior is the same from PowerShell,
Linux, and macOS. The definitive gate checks module and vendor reproducibility,
builds every published tool, tests the compiler/CLI/LSP boundary, rejects stale
monorepository imports, enforces pinned formatting, lint, nil-safety, security,
race, fuzz, and 85% coverage gates, and generates a third-party SDK fixture
twice from zero before compiling and executing it offline.

The third-party fixture under `testdata/` is handwritten. Its generated output
is deliberately untracked and recreated by verification.

## Extraction provenance

The repository retains the filtered history of the compiler/tooling boundary.
Its public-core bridge is pinned to
`github.com/spice-framework/spice@v0.0.0-20260805222830-a2ecd56df246`
(commit `a2ecd56df246ad3a647b64b0585738a2495ecf5c`). The Apache-2.0 license and
pinned quality-tool versions were carried from the extracted source history,
then the module identities and standalone gate were adapted here. No migration
script or machine-specific replacement is part of the published tree.

## Extraction status

This initial standalone boundary intentionally runs `cmd/spice` through the
handwritten CLI, exactly like `cmd/spice-bootstrap`. The former monorepository's
stale generated self-hosted command was removed rather than published under the
wrong module identity. Production self-hosting and release benchmark policy are
follow-up integration milestones; this repository does not claim them yet.

## License

Apache License 2.0. See [LICENSE](LICENSE).
