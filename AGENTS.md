# Spice Toolchain Implementation Contract

This repository owns the Spice compiler, generator, CLI, LSP, scaffold, and
official annotation executable. Public runtime and descriptor contracts belong
to `github.com/spice-framework/spice`.

## Working model

- Work directly on local `main` in single-writer mode.
- Fetch before a push and never overwrite unexpected remote work.
- Require exactly Go 1.26.5.
- Keep commits bounded and green.
- Do not hand-edit generated files or vendor contents.
- Do not reintroduce monorepository compiler, internal, or command imports under
  `github.com/spice-framework/spice`.

## Boundary invariants

- `cmd/spice`, `cmd/spice-bootstrap`, and `cmd/spice-annotation-core` build from
  handwritten packages without production generated code.
- Generated application Go may import public core packages but never this
  module's compiler or CLI internals.
- Only the exact official core-descriptor/toolchain-tool pairing may cross the
  descriptor/tool module boundary. Third-party descriptors and tools must share
  one resolved module identity.
- Temporary module fixtures resolve the pinned public core through Go module
  provenance; committed files never contain machine-specific replacements.
- Analysis and editor operations remain offline and do not execute provider or
  descriptor bodies.
- `spice-library-release-verify` must remain independent of every release
  builder. It authenticates signed library artifacts against an external trust
  anchor and exact Git objects; it never re-renders artifacts or trusts the
  emitted public key as its own anchor.

## Feedback loop

Use `make fast` while editing and `make check` for the broader package boundary.
Run `make verify` once on the exact tree to commit. The final gate owns tidy,
vendor, vet, offline, identity, tool-build, and third-party generation proofs.
Never claim a gate passed unless it was executed.

Production self-hosting remains separate follow-up work. Toolchain performance
budgets are repository-owned, run independently with `make benchmark`, and are
mandatory in `make verify`. Release artifacts follow the guarded contract in
`RELEASING.md`.
Do not revive the removed stale generated command or monorepository acceptance
tests as placeholders.
