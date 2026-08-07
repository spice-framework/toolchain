# Java-structured profile

Spice keeps application source valid Go. The optional `java-structured`
profile narrows handwritten production source to a class-oriented subset in
which named types own behavior and generated Go still uses direct calls.

Enable the contract at the command line:

```text
go tool github.com/spice-framework/toolchain/cmd/spice verify --profile=java-structured ./...
```

For a new application, the scaffold creates the matching entry-point and
module-root layout directly:

```text
go tool github.com/spice-framework/toolchain/cmd/spice init --module example.com/shop --profile=java-structured
go tool github.com/spice-framework/toolchain/cmd/spice new module orders
go tool github.com/spice-framework/toolchain/cmd/spice new service OrderService --directory internal/orders --package orders
```

All typed declaration kinds accept `--profile=java-structured`; their current
templates already satisfy that strict contract. Scaffolding is additive and
fails if its destination file exists.

Editor clients can pass the same value as the Spice LSP initialization or
workspace setting:

```json
{"spice":{"profile":"java-structured"}}
```

The profile enforces these source invariants:

- each handwritten non-test, non-generated production file declares at most
  one named type;
- every handwritten receiver method lives in the file that declares its
  receiver type;
- package functions are limited to `main` in package `main` and same-file
  type-associated forms `New<Type>`, `Parse<Type>`, `Must<Type>`, and
  `<Type>From...`;
- package-level `@Bean` providers and function-owned event topics are rejected;
- constructible stereotypes use `New<Type>` in the type's file or generated
  zero-value allocation; generic `New` and custom constructor names are
  rejected;
- `package.go` contains only package documentation, annotation/import
  declarations, and the package clause;
- `init` functions and mutable package variables are rejected;
- when a managed type satisfies a named interface owned by the application,
  the relationship is declared explicitly with `@Implements`.

Violations use stable `spice.style.*` diagnostic codes and are compilation
errors only when the profile is selected. Ordinary Spice analysis continues to
accept the full valid-Go source model.

## Closed enums

`@Enum` is a compiler contract independent of the style profile. One enum file
contains one exported, non-generic string or integer named type and all of its
exactly typed constants. Members cannot be extended from another file or reuse
an underlying value. Other constants do not belong in the enum file.

The language server offers **Generate enum helpers** on a validated enum type.
It inserts only missing `Parse<Type>`, `String`, and `Valid` declarations into
the handwritten enum file and adds a safe `fmt` import when required. These are
source edits because Go forbids a generated package from attaching methods to
an application-owned type.

## Service-centered cross-cutting behavior

Cross-cutting application behavior belongs on managed service methods. A
service using transaction, authorization, cache, retry, or observation policy
must declare exactly one application interface with `@Implements`, use
singleton scope, accept exact `context.Context` first, and return `error` last.
Constructors inject that interface. Concrete injection is rejected because it
would bypass generated policy behavior.

Generated decorators nest policies as observation → authorization → cache →
retry → transaction → direct target. They are ordinary target-scoped Go with
direct method calls; the profile does not authorize reflection or runtime AOP.

## What the profile does not add

The profile does not introduce field injection, runtime scanning, reflection
dependency lookup, runtime proxies, service locators, mutable application
contexts, or inheritance emulation. Constructor injection, exact Go type
identity, explicit interface binding, compile-time validation, and ordinary
generated Go remain the runtime model.
