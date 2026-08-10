# Spice Java-Structured Go Code Style

**File:** `CODE_STYLE.md`
**Status:** Normative for application code
**Profile name:** `java-structured`
**Reviewed Spice baseline:** `spice-framework/spice@53b0098533b2fb4002db2de990d87dced73dc33f`
**Reviewed Toolchain baseline:** `spice-framework/toolchain@bab8bcaf7d0c6311237b34812c681c3ee6a6593b`
**Reviewed reference application:** `spice-framework/petclinic@7e5a91efaa6f56a9c887ddac3d44304dc232a406`

---

## 1. Purpose

This guide defines how to write a Spice application that has the structural clarity of a well-designed Java application while preserving Go's compiler, tooling, deployment model, and direct-call performance.

The intended result is:

- one primary named type per handwritten source file;
- application behavior owned by named structs rather than loose package functions;
- explicit constructor injection;
- explicit module boundaries;
- explicit interfaces and implementation bindings;
- explicit bean lifetimes;
- generated wiring instead of reflection or a service locator;
- package-by-feature organization;
- readable files whose purpose is obvious from the filename;
- architecture rules that fail in the editor and CI instead of depending on code review.

This is **not** an attempt to reproduce every Java language feature. It is a disciplined Go profile built around Spice.

The governing principle is:

> **Use Java-shaped architecture and Go-native semantics.**

---

## 2. Normative language

The words **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT**, and **MAY** are normative.

- **MUST / MUST NOT** rules are intended to fail automated verification.
- **SHOULD / SHOULD NOT** rules should normally produce warnings.
- **MAY** rules are optional.

Generated files, vendored files, and third-party source are outside this guide unless explicitly stated.

---

## 3. Non-goals

This profile does not introduce or encourage:

- Java syntax in invalid Go source;
- inheritance hierarchies;
- getters and setters for every field;
- runtime reflection-based dependency injection;
- a mutable application context;
- bean lookup by string;
- field or setter injection;
- package scanning at runtime;
- Maven-style `src/main/go` and `src/test/go` directories;
- exceptions in place of Go errors;
- a fake class hierarchy around standard-library values;
- empty “utility classes” used only to satisfy a rule;
- hidden global worker pools, schedulers, loggers, or event buses.

Spice annotations remain valid Go comments in canonical `// @Annotation` form.

---

# Part I — Source structure

## 4. One primary named type per file

Every handwritten non-test `.go` file MUST contain exactly one primary named type unless it is one of the explicit boundary-file exceptions in this guide.

A primary named type includes:

- `struct`;
- `interface`;
- a defined scalar or collection type;
- a generic named type;
- a type alias;
- a Go-style enum type.

The following is forbidden:

```go
// order.go
type Order struct {
    ID OrderID
}

type OrderID string

type OrderStatus string
```

The required layout is:

```text
order.go
order_id.go
order_status.go
```

### 4.1 Allowed declarations in a type file

A type file MAY contain only declarations directly owned by its primary type:

- the primary named type;
- its methods;
- its constructor and alternate constructors;
- enum constants of that type;
- constants that define the type's invariant;
- compile-time assertions for that type when Spice cannot generate them;
- private implementation details inside method bodies;
- imports and annotation imports;
- documentation.

A type file MUST NOT contain:

- a second named type;
- unrelated package functions;
- unrelated package constants;
- package mutable state;
- constructors for another type;
- methods whose receiver is another type.

### 4.2 Methods stay with their type

Every handwritten method MUST be declared in the same file as its receiver's primary type.

Forbidden:

```text
order_service.go
order_service_create.go
order_service_cancel.go
```

Required:

```text
order_service.go
```

If one type cannot remain understandable in one file, split the responsibility into multiple collaborating types. Do not split one class-like type across files.

A type file SHOULD remain below 400 lines and MUST trigger a maintainability warning at 500 lines. Generated files are exempt.

### 4.3 Boundary-file exceptions

The following files MAY contain no primary type:

| File category | Allowed declarations |
|---|---|
| `doc.go` | Package documentation and package annotations |
| `main.go` | The exact application entrypoint |
| `*_bean.go` | One approved Spice `@Bean` provider function |
| `*_topic.go` | One approved `@event.Topic` marker function |
| `*_test.go` | Go test, benchmark, fuzz, and example functions |
| generated file | Generator-owned declarations |
| cgo/protobuf/tool output | Tool-owned declarations |
| `package_constants.go` | Rare package protocol constants approved by architecture review |

A boundary exception is not permission to collect random helpers in a file.

### 4.4 Filename rule

The filename MUST be the initialism-aware snake-case form of the primary type name.

| Type | File |
|---|---|
| `Order` | `order.go` |
| `OrderService` | `order_service.go` |
| `HTTPController` | `http_controller.go` |
| `OrderID` | `order_id.go` |
| `OIDCConfiguration` | `oidc_configuration.go` |
| `PostgresOrderRepository` | `postgres_order_repository.go` |

An operating-system or architecture implementation MAY append one exact Go
build suffix after the primary type name: for example,
`platform_resolver_windows.go`, `unix_process_unix.go`, or
`native_launcher_linux_arm64.go`. The suffix must be a supported `GOOS`, a
supported `GOARCH`, an exact `GOOS_GOARCH` pair, or the reviewed `unix` family
used with an explicit `//go:build linux || darwin` constraint. Arbitrary role
suffixes such as `_helper`, `_impl`, or `_fast` remain forbidden.

Recognized initialisms SHOULD include at least:

```text
API, ASCII, CPU, CSS, DNS, EOF, GUID, HTML, HTTP, HTTPS, ID, IP,
JSON, JWT, QPS, RAM, RPC, SLA, SMTP, SQL, SSH, TCP, TLS, TTL,
UDP, UI, UID, URI, URL, UTF8, UUID, VM, XML, XMPP, XSRF, XSS
```

### 4.5 Grouped type declarations are forbidden

Do not use:

```go
type (
    Order struct{}
    OrderID string
)
```

Each type gets its own file and its own declaration.

---

## 5. Application behavior belongs to structs

In this profile, “everything is a struct” means every concrete application component that owns behavior is a named struct. Contracts remain interfaces, enums remain named scalar types, and immutable identifiers/value objects use the Go type that best preserves their invariant.

All stateful or domain-significant application behavior MUST be owned by a named struct receiver.

Preferred:

```go
type PriceCalculator struct {
    taxPolicy TaxPolicy
}

func (calculator *PriceCalculator) Calculate(
    ctx context.Context,
    order Order,
) (Money, error) {
    // ...
}
```

Forbidden:

```go
func CalculatePrice(
    ctx context.Context,
    taxPolicy TaxPolicy,
    order Order,
) (Money, error) {
    // ...
}
```

### 5.1 What counts as application behavior

The rule applies to:

- use cases;
- application services;
- domain services;
- repositories;
- adapters;
- controllers;
- validators with dependencies;
- schedulers;
- event listeners;
- asynchronous tasks;
- infrastructure owners;
- protocol clients;
- mappers with meaningful policy;
- orchestration;
- business calculations.

### 5.2 Do not invent utility classes

Do not replace a loose function with an empty `XUtils` struct merely to satisfy the rule.

Forbidden:

```go
type StringUtils struct{}

func (StringUtils) Trim(value string) string {
    return strings.TrimSpace(value)
}
```

Use one of these instead:

1. Put behavior on the domain value it belongs to.
2. Put private helper behavior on the owning service.
3. Create a real stateless service with a meaningful role and inject it.
4. Use a local closure when the logic is truly local.
5. Use an approved package function only when Go's language model requires it.

### 5.3 Pure value behavior

Behavior intrinsic to a value SHOULD be a value-receiver method:

```go
type EmailAddress string

func (address EmailAddress) Domain() string {
    // ...
}
```

### 5.4 Generic algorithms

Go does not allow methods to declare additional type parameters. A genuinely reusable generic algorithm MAY remain a package function only when all of the following are true:

- it cannot naturally belong to an existing named type;
- it is stateless and deterministic;
- it is placed in a narrowly named package such as `collection` or `algorithm`;
- it is covered by tests;
- it has an explicit style-policy exception;
- its name describes the algorithm, not a vague helper role.

This is a language exception, not a routine escape hatch.

---

## 6. Approved package-level functions

Package-level functions are denied by default.

The following categories are allowed.

### 6.1 Process entrypoint

```go
func main()
```

It MUST exist only in a `package main` command boundary and SHOULD contain only argument/exit handling and the generated Spice target call.

### 6.2 Constructors and type-associated factories

The following forms are associated with the primary type in the same file:

```text
New<Type>
New<Type>From<Source>
Parse<Type>
```

Examples:

```go
func NewOrder(...) (*Order, error)
func NewOrderFromSnapshot(...) (Order, error)
func ParseOrderID(value string) (OrderID, error)
```

Rules:

- the function MUST be in the primary type's file;
- the returned primary value MUST be the first result;
- additional results MAY only be `lifecycle.Cleanup` and/or `error` in Spice-supported order;
- `error` MUST be last;
- `MustNew...` and `MustParse...` are forbidden outside tests and static program initialization;
- managed Spice types MUST use exactly `New<Type>` as their primary constructor.

### 6.3 Spice provider marker

One package-level `@Bean` function is allowed in a dedicated `*_bean.go` file when an exact external or framework-owned type must be provided.

This is an infrastructure boundary, not ordinary application structure.

### 6.4 Spice event-topic marker

One package-level `@event.Topic` marker is allowed in a dedicated `*_topic.go` file.

The function body MUST remain the minimal compiler marker required by the pinned Spice version and MUST contain no application behavior.

### 6.5 Go test entrypoints

The following are allowed in `_test.go` files:

```text
Test*
Benchmark*
Fuzz*
Example*
TestMain
```

Unexported test helpers are permitted, although repeated test orchestration SHOULD move into a named test harness struct.

### 6.6 Tool-required functions

Generated registration functions, cgo bridges, protobuf/grpc registration, plugin entrypoints, or other tool-required forms MAY be allowed only through an exact file-and-symbol exception.

### 6.7 Forbidden package functions

The following are always violations unless generated:

```go
func init()
func validateOrder(...)
func mapOrder(...)
func calculateTax(...)
func retry(...)
func loadConfiguration(...)
func startWorker(...)
func getGlobalClient(...)
```

Move that behavior to a type, a constructor, a receiver method, or an explicit supported boundary.

---

## 7. Package state is forbidden

Application packages MUST NOT declare mutable package variables.

Forbidden:

```go
var database *sql.DB
var logger = slog.Default()
var cache = map[string]Order{}
var once sync.Once
```

Also forbidden:

- package-level service singletons;
- mutable registries;
- package-level context values;
- global event buses;
- global schedulers;
- global HTTP clients configured by mutation;
- global random generators used concurrently without explicit ownership;
- global feature flags.

Prefer:

- constructor-injected dependencies;
- immutable constants;
- generated Spice singleton ownership;
- explicit application options;
- scoped beans;
- test overrides.

### 7.1 Sentinel errors

New application code SHOULD use typed errors rather than mutable-looking package sentinel variables.

Preferred:

```go
type OrderNotFoundError struct {
    OrderID OrderID
}

func (err OrderNotFoundError) Error() string {
    return "order not found"
}
```

A stable public sentinel MAY be retained only when interoperability with `errors.Is` requires it and the API has already committed to that shape.

### 7.2 `init` is forbidden

`func init()` is forbidden in handwritten application code.

Construction, registration, validation, scheduling, and startup MUST be explicit through Spice generation, constructors, or lifecycle methods.

---

# Part II — Naming and type design

## 8. Package naming

Package names MUST be:

- lowercase;
- singular;
- short but specific;
- free of underscores;
- free of generic buckets such as `util`, `utils`, `helpers`, `common`, or `misc`.

Preferred:

```text
orders
payments
identity
catalog
shipping
```

Avoid global layer packages:

```text
controllers
services
repositories
models
```

The repository is organized by feature/module, not by technical layer.

---

## 9. Type naming

Use role suffixes when they add architectural meaning.

| Role | Preferred suffix |
|---|---|
| Application behavior | `Service` |
| Data access | `Repository` |
| HTTP entrypoint | `Controller` |
| Typed settings | `Configuration` |
| Inbound transport | `Request`, `Command`, `Query` |
| Outbound transport | `Response`, `View` |
| Application/domain event | past-tense event name, optionally `Event` |
| Error | `Error` |
| External integration | product/protocol + role |
| Scheduled behavior | `Job` or meaningful service name |
| Policy | `Policy` |
| Factory with real state/behavior | `Factory` |
| Mapper with real policy | `Mapper` |
| Lifecycle resource owner | concrete resource role |

Avoid:

```text
OrderServiceImpl
IOrderService
BaseService
AbstractRepository
OrderHelper
OrderUtils
CommonManager
GeneralProcessor
DataHandler
```

`Manager` SHOULD be used only for a type that truly coordinates a lifecycle or aggregate of resources.

---

## 10. Struct fields

Managed component fields MUST be private.

Preferred:

```go
type OrderService struct {
    repository OrderRepository
    publisher  event.Publisher[OrderCreated]
}
```

Forbidden:

```go
type OrderService struct {
    Repository OrderRepository
    Publisher  event.Publisher[OrderCreated]
}
```

Exported fields are normally limited to:

- wire DTOs;
- immutable API records;
- configuration structs;
- domain records whose direct field access is an intentional invariant;
- generated compatibility surfaces.

Do not use setter injection:

```go
func (service *OrderService) SetRepository(repository OrderRepository)
```

Dependencies are constructor parameters.

---

## 11. Receiver rules

Receiver names MUST be short, meaningful, and consistent across every method of a type.

Preferred:

```go
func (service *OrderService) Create(...)
func (repository *PostgresOrderRepository) Save(...)
func (controller *OrderController) Get(...)
func (status OrderStatus) Valid() bool
```

Avoid one-letter receivers for application types:

```go
func (s *OrderService) Create(...)
func (r *PostgresOrderRepository) Save(...)
```

One-letter receivers MAY be used only for universally obvious tiny value types.

### 11.1 Pointer versus value receivers

Use pointer receivers for:

- managed Spice beans;
- identity-bearing entities;
- types that mutate;
- types that own resources;
- types too large to copy;
- types whose methods must share one method set consistently.

Use value receivers for:

- enums;
- small immutable value objects;
- request validation;
- immutable identifiers;
- immutable error values where appropriate.

A type MUST NOT mix pointer and value receivers without a documented reason.

---

## 12. Interfaces

Interfaces are contracts, not implementation containers.

Create an interface only when at least one of these is true:

- it is a cross-module port;
- more than one implementation exists or is expected;
- a test substitute is materially useful;
- it isolates an external dependency;
- it defines a stable public capability.

Rules:

- one interface per file;
- no `I` prefix;
- no `Impl` suffix on implementations;
- keep interfaces small and consumer-owned;
- do not export an interface solely because a struct exists;
- do not use `interface{}` or `any` in application contracts without a protocol-level reason;
- bind concrete Spice beans explicitly with `@Implements`.

Preferred pairing:

```text
order_repository.go              -> interface OrderRepository
postgres_order_repository.go     -> struct PostgresOrderRepository
```

Preferred capability split:

```go
type OrderCreator interface {
    Create(context.Context, CreateOrderCommand) (OrderView, error)
}
```

instead of a broad catch-all interface with unrelated methods.

---

## 13. Enum style

Go has no enum declaration keyword. This profile represents enums with one named scalar type and a typed constant group in the same file.

```go
type OrderStatus string

const (
    OrderStatusUnknown   OrderStatus = ""
    OrderStatusPending   OrderStatus = "pending"
    OrderStatusConfirmed OrderStatus = "confirmed"
    OrderStatusCancelled OrderStatus = "cancelled"
)

func (status OrderStatus) Valid() bool {
    switch status {
    case OrderStatusPending,
        OrderStatusConfirmed,
        OrderStatusCancelled:
        return true
    default:
        return false
    }
}
```

Rules:

- the zero value SHOULD be a safe explicit unknown/unset value;
- wire- or storage-visible enums SHOULD use explicit string or integer values;
- avoid `iota` when persisted or transmitted values must remain stable;
- validation and parsing behavior belong in the enum file;
- enum methods use value receivers;
- the file contains no second named type.

Use exhaustive switches and enable the `exhaustive` linter.

---

## 14. Value objects and strong IDs

Primitive values with domain meaning SHOULD use named types.

Preferred:

```go
type OrderID string

func NewOrderID(value string) (OrderID, error) {
    // validate invariant
}
```

Avoid passing unclassified primitives throughout the application:

```go
func Find(ctx context.Context, id string) (...)
```

Prefer:

```go
func (repository *OrderRepository) Find(
    ctx context.Context,
    id OrderID,
) (...)
```

Do not create getters merely to imitate Java. Expose behavior and invariants, not boilerplate.

---

## 15. DTOs are not domain models

HTTP, messaging, persistence, and public API DTOs MUST be separate from domain entities when their contracts differ.

Each request, response, command, query, view, and event type gets its own file.

Examples:

```text
create_order_request.go
create_order_response.go
create_order_command.go
order_view.go
order_created.go
```

Transport tags do not belong on core domain entities unless the domain type is intentionally the public wire contract.

---

## 16. Error style

Errors MUST:

- end in `Error` when represented by a type;
- contain safe structured context;
- avoid secrets and raw untrusted input in `Error()`;
- preserve causes with `%w` or `Unwrap`;
- be checked with `errors.Is` and `errors.As`;
- appear as the last return value;
- not be logged and returned at every layer.

A component logs an error only when it owns the final handling boundary or adds meaningful operational information that will not be duplicated.

Do not use panic for expected application failures.

---

# Part III — Repository architecture

## 17. Package by feature and Spice module

The default repository structure is package-by-feature.

```text
.
├── cmd/
│   └── shop/
│       ├── doc.go
│       └── main.go
├── internal/
│   ├── orders/
│   │   ├── doc.go
│   │   ├── order.go
│   │   ├── order_id.go
│   │   ├── order_status.go
│   │   ├── order_repository.go
│   │   ├── postgres_order_repository.go
│   │   ├── order_service.go
│   │   ├── order_controller.go
│   │   ├── create_order_request.go
│   │   ├── create_order_response.go
│   │   ├── order_created.go
│   │   ├── order_created_topic.go
│   │   └── api/
│   │       ├── doc.go
│   │       └── order_creator.go
│   ├── payments/
│   │   ├── doc.go
│   │   └── ...
│   ├── platform/
│   │   ├── doc.go
│   │   └── ...
│   └── spicegen/
│       └── shop/
├── test/
│   ├── acceptance/
│   └── integration/
├── tools/
├── go.mod
├── go.sum
└── CODE_STYLE.md
```

Do not introduce:

```text
src/main/go
src/test/go
internal/controllers
internal/services
internal/repositories
internal/models
```

### 17.1 Module root

Every application feature MUST be owned by exactly one package-level `@Module`.

A module's `doc.go` is the architectural equivalent of a Java package/module descriptor.

```go
// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package orders owns ordering behavior.
//
// @Module(allowedDependencies=["example.com/shop/internal/payments::api", "example.com/shop/internal/platform"])
package orders
```

Rules:

- dependency paths are explicit;
- every used cross-module edge MUST be declared;
- unused declared edges SHOULD fail or warn;
- cycles are forbidden;
- unassigned packages are forbidden;
- module roots use full import paths;
- implementation descendants are not public by implication.

### 17.2 Named interfaces

Use `@NamedInterface` for a deliberately exposed descendant package.

```go
// @import { NamedInterface } from "github.com/spice-framework/spice/annotation/modulith"

// Package api exposes the supported ordering use cases.
//
// @NamedInterface("api")
package api
```

Named interfaces SHOULD be used for:

- stable cross-module use-case contracts;
- published events;
- deliberately exposed SPI contracts.

Do not expose a package merely because another module currently imports an implementation detail.

### 17.3 Keep modules cohesive

Start with one Go package per Spice module. Split descendants only when there is a real boundary, not to reproduce Java's folder depth.

A module SHOULD normally own:

- domain types;
- its application service;
- repository contracts;
- local repository implementations;
- controllers;
- DTOs;
- events;
- module-specific configuration.

### 17.4 Command package

The command package is an assembly boundary, not a business module.

The selected command package MUST also be an explicit assembly `@Module` so the package is not left unassigned:

```go
// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package main assembles the shop application.
//
// @Module(allowedDependencies=["example.com/shop/internal/orders", "example.com/shop/internal/payments", "example.com/shop/internal/platform"])
package main
```

It owns:

- package documentation;
- the `@Application` marker;
- explicit blank imports of participating local modules;
- the generated target import;
- process exit conversion.

It MUST NOT own business logic, providers, controllers, repositories, or configuration parsing.

```go
package main

import (
    "os"

    _ "example.com/shop/internal/orders"
    _ "example.com/shop/internal/payments"
    _ "example.com/shop/internal/platform"
    spiceapp "example.com/shop/internal/spicegen/shop"
)

// @import { Application } from "github.com/spice-framework/spice/annotation/core"
// @import { Enable } from "github.com/spice-framework/spice/annotation/management"
// @import { Logging } from "github.com/spice-framework/spice/annotation/observability"

// @Application
// @Enable(expose=["health", "liveness", "readiness", "info", "metrics", "modules"], access="loopback")
// @Logging
func main() {
    os.Exit(spiceapp.Main(os.Args[1:]))
}
```

`os.Exit` is allowed only at this process boundary.

### 17.5 Generated code

Generated Spice source MUST:

- live under `internal/spicegen/<target>`;
- be committed when the application repository's release process requires reproducible generation;
- be checked with `spice generate --check`;
- never be manually edited;
- remain covered by the Spice ownership manifest;
- be excluded from handwritten structural rules.

---

# Part IV — Spice annotation policy

## 18. Annotation source-of-truth policy

The annotation descriptor source and pinned compiler behavior are authoritative.

The reviewed `docs/annotations.md` summary does not contain every argument currently present in the descriptor source. In particular, the current descriptors add or clarify:

- `@Bean(name=..., aliases=[...])`;
- `@Service(constructor=..., name=..., aliases=[...])`;
- `@Repository(constructor=..., name=..., aliases=[...])`;
- `@Controller(prefix=..., constructor=..., name=..., aliases=[...])`;
- `@security.Authorize(expression=...)`;
- `@management.Enable(access="public"|"loopback")`;
- default values on several descriptors.

Projects MUST pin compatible Spice core/toolchain versions and MUST run `spice verify`. Do not infer behavior from annotation spelling alone.

Known drift in the reviewed baseline:

- `docs/annotations.md` omits newer bean identity and constructor arguments that exist in descriptor source.
- `docs/web.md` still contains an older statement that a controller requires a separate exact `@Bean`, while the current `@Controller` descriptor and compiler provider model make controllers constructible stereotypes.
- event-topic examples are not identical across every document revision; use the signature accepted by the pinned compiler and current descriptor source.

Resolve conflicts in this order:

1. pinned descriptor source and typed contribution metadata;
2. pinned compiler validation/generation behavior;
3. executable reference and acceptance tests;
4. narrative documentation;
5. this style guide.

---

## 19. Canonical annotation imports

Every annotated file MUST declare its own exact `@import` bindings.

Preferred:

```go
// @import { Implements, Service, Singleton } from "github.com/spice-framework/spice/annotation/core"
// @import * as ordersapi from "example.com/shop/internal/orders/api"
```

Rules:

- imports are file-scoped;
- each annotation invocation MUST appear on one comment line;
- annotation argument lists MUST NOT be wrapped across several comment lines;
- use named bindings for official annotations;
- use namespace imports for interface type expressions or name collisions;
- sort annotation import lines by package path;
- sort named symbols alphabetically;
- do not use retired `@spice.import`;
- do not rely on package-level annotation carryover;
- do not duplicate a local annotation binding;
- use canonical `// @...` form.

---

## 20. Annotation order

### 20.1 Managed type

Use this order:

1. stereotype;
2. explicit scope;
3. explicit interface bindings;
4. qualifier;
5. primary/fallback;
6. collection order.

```go
// OrderService coordinates order use cases.
//
// @Service(constructor=NewOrderService)
// @Singleton
// @Implements(ordersapi.OrderCreator)
// @Qualifier("orders")
// @Primary
// @Order(10)
type OrderService struct {
    // ...
}
```

Not every type needs all annotations. Do not add selection metadata without a real ambiguity.

### 20.2 Controller method

Use this order:

1. route;
2. security;
3. transaction;
4. cache, only where compatible.

```go
// @Post("/")
// @Authorize(authenticated=true, allScopes=["orders.write"])
// @Transactional(isolation="serializable")
func (controller *OrderController) Create(...) (...)
```

The compiler intentionally rejects unsafe combinations such as authorization-sensitive or transactional cache boundaries.

### 20.3 Application function

Use this order:

1. `@Application`;
2. `@Enable`;
3. `@Logging`.

### 20.4 Lifecycle and background methods

Use the single behavior annotation immediately above the method:

```go
// @OnStart
func (server *HTTPServer) Start(context.Context) error
```

---

## 21. Complete official annotation inventory

The reviewed Spice baseline exposes 35 official descriptors.

| Annotation | Target | Current arguments/contract | Project rule |
|---|---|---|---|
| `@Application` | function | marker | Only the command `main` function |
| `@Bean` | function | optional `name`, `aliases` | Exception-only provider boundary |
| `@Component` | type | optional `constructor`, `name`, `aliases` | Infrastructure/support component with no narrower stereotype |
| `@Configuration` | type | optional `prefix` | One typed settings struct per file |
| `@ConfigurationProperties` | type | optional `prefix` | Compatibility spelling; prefer the project-selected canonical configuration descriptor consistently |
| `@Enum` | type | marker | Named scalar enum with same-file typed constants |
| `@Service` | type | optional `constructor`, `name`, `aliases` | Application/domain orchestration |
| `@Repository` | type | optional `constructor`, `name`, `aliases` | Data-access implementation |
| `@Implements` | type/function | one or more named interfaces | Required for concrete interface exposure |
| `@Qualifier` | type/function/parameter | repeatable required string | Prefer for semantic selection |
| `@Primary` | type/function | marker | Rare; one preferred implementation |
| `@Fallback` | type/function | marker | Auto-configuration/default only |
| `@Order` | type/function | required integer | Collection injection only |
| `@Singleton` | type/function | marker | Explicit default for managed components |
| `@Prototype` | type/function | marker | Fresh caller-owned acquisition |
| `@RequestScope` | type/function | marker | Request-owned instance |
| `@SessionScope` | type/function | marker | Session-owned instance |
| `@Module` | package | optional `allowedDependencies` | Required for each application module |
| `@NamedInterface` | package | repeatable required name | Explicit descendant API |
| `@Controller` | type | `prefix`, `constructor`, `name`, `aliases` | Typed HTTP adapter owner |
| `@Get` | method | required `path` | Typed or explicit raw GET |
| `@Post` | method | required `path` | Typed or explicit raw POST |
| `@OnStart` | method | exact lifecycle signature | Start resource-owning component |
| `@OnStop` | method | exact lifecycle signature | Stop component that also starts |
| `@async.Execute` | method | marker | Generated bounded asynchronous boundary |
| `@schedule.FixedDelay` | method | `delay`, `initialDelay`, `continueOnError` | Generated non-overlapping schedule |
| `@event.Listener` | method | optional `order` | Typed application-event listener |
| `@event.Topic` | function | marker | Dedicated marker-only file |
| `@cache.Cacheable` | method | required stable `name` | Safe typed GET reads only |
| `@data.Transactional` | method | `isolation`, `readOnly` | Explicit generated transaction boundary |
| `@security.Authorize` | method | auth, roles, scopes, restricted expression | Protected HTTP route |
| `@observability.Logging` | function | marker | Application entrypoint |
| `@observability.Observed` | method | optional stable `name` | Generated instance-owned operation observation |
| `@retry.Retryable` | method | bounded attempts/backoff/classifier | Explicit finite retry decorator |
| `@management.Enable` | function | required `expose`, optional `access` | Minimal explicit management surface |

---

## 22. Stereotype selection

Use the most specific stereotype.

### 22.1 `@Service`

Use for:

- application use-case orchestration;
- domain service behavior requiring injected dependencies;
- workflow coordination;
- listeners/jobs whose primary role is application behavior.

Do not use `@Service` for every injectable object.

### 22.2 `@Repository`

Use for:

- persistence adapters;
- query execution;
- storage access;
- repository implementations.

Repository interfaces themselves are not beans unless an exact provider returns them. Concrete implementations use `@Repository` plus `@Implements`.

### 22.3 `@Controller`

Use only for HTTP adapter owners. Business rules remain in services/domain types.

Controllers SHOULD:

- bind and validate transport input;
- classify authorization;
- establish supported generated transaction boundaries;
- call one or more application services;
- map results to response DTOs/views.

Controllers SHOULD NOT implement domain decisions or direct SQL.

### 22.4 `@Configuration`

Use for typed properties, not for a Java-style factory class.

Every exported field MUST have an explicit `spice` tag or `spice:"-"`.

```go
// @import { Configuration } from "github.com/spice-framework/spice/annotation/core"

// ServerConfiguration defines HTTP server properties.
//
// @Configuration(prefix="server")
type ServerConfiguration struct {
    Address         string        `spice:"address,default=127.0.0.1:8080"`
    ShutdownTimeout time.Duration `spice:"shutdown-timeout,default=10s"`
    Token           string        `spice:"token,required,secret"`
}
```

Configuration values are immutable application inputs. Components MUST NOT call `os.Getenv` directly.

### 22.5 Generic components

Use the official constructible `@Component` for infrastructure and support
components that do not honestly have service, repository, controller, or
configuration semantics.

Choose in this order:

1. assign the real semantic role `@Service`, `@Repository`, or `@Controller`;
2. use `@Component` for a meaningful application-owned support component;
3. wrap an external resource in a meaningful resource-owning struct;
4. use an exception-only `@Bean` provider for an exact external type.

Do not misuse `@Service` merely because a type needs injection, and do not use
`@Component` to avoid choosing a more precise available stereotype.

---

## 23. Explicit constructors

Every managed `@Service`, `@Repository`, and `@Controller` MUST declare an explicit constructor argument even though Spice can discover constructors conventionally.

```go
// @Service(constructor=NewOrderService)
type OrderService struct {
    repository OrderRepository
}

func NewOrderService(
    repository OrderRepository,
) (*OrderService, error) {
    return &OrderService{
        repository: repository,
    }, nil
}
```

This rule deliberately avoids changes in wiring caused by adding another `New` function.

The primary constructor MUST:

- be named `New<Type>`;
- be in the same file as the type;
- list dependencies explicitly;
- return the concrete type first;
- return `error` last when validation can fail;
- return `lifecycle.Cleanup` only when it truly owns cleanup;
- perform no hidden package registration;
- start no goroutines;
- read no environment variables;
- install no signal handlers;
- use no service locator.

### 23.1 Accepted Spice constructor shapes

```go
func(dependencies...) T
func(dependencies...) (T, error)
func(dependencies...) (T, lifecycle.Cleanup)
func(dependencies...) (T, lifecycle.Cleanup, error)
```

Managed identity/resource types SHOULD return pointers.

### 23.2 Generated `new(T)` fallback

The strict profile forbids relying on generated `new(T)` construction for managed application components. Explicit constructors are required even for zero-dependency components.

This preserves one visible construction boundary and makes later dependencies a deliberate change.

---

## 24. Constructor injection only

Dependencies MUST enter through constructors.

Forbidden:

- field injection;
- setter injection;
- global lookup;
- context-based dependency lookup;
- package variable lookup;
- `ApplicationContext.Get("name")`;
- reflection-based injection;
- lazy initialization hidden in a method.

When optional or deferred wiring is real, use Spice's typed:

```text
bean.Optional[T]
bean.Lazy[T]
bean.Provider[T]
```

Do not use nil as an undocumented optional dependency.

---

## 25. Interface bindings

Spice intentionally does not treat ordinary Go assignability as an implicit bean declaration.

A concrete managed component consumed as an interface MUST use `@Implements` unless its provider returns the exact interface type.

```go
// @import { Implements, Repository, Singleton } from "github.com/spice-framework/spice/annotation/core"
// @import * as ordersapi from "example.com/shop/internal/orders/api"

// PostgresOrderRepository stores orders in PostgreSQL.
//
// @Repository(constructor=NewPostgresOrderRepository)
// @Singleton
// @Implements(ordersapi.OrderRepository)
type PostgresOrderRepository struct {
    // ...
}
```

Rules:

- use typed interface expressions;
- let Spice generate the compile-time assertion;
- do not add redundant handwritten assertions when Spice owns them;
- do not rely on parameter-name magic when an explicit qualifier is clearer;
- do not return an interface merely to hide a concrete implementation from Spice.

---

## 26. Bean identity and selection

### 26.1 Names and aliases

Bean `name` and `aliases` are allowed but SHOULD be rare.

Use them for:

- stable integration identity;
- migration compatibility;
- map injection keys;
- a deliberately named application capability.

Do not use names as a substitute for types or qualifiers.

### 26.2 Qualifiers

Use `@Qualifier` when multiple implementations represent different semantics.

```go
// @Qualifier("stripe")
type StripePaymentProcessor struct {
    // ...
}
```

```go
func NewCheckoutService(
    // @Qualifier("stripe")
    processor PaymentProcessor,
) *CheckoutService {
    // ...
}
```

Qualifier strings MUST be stable, lowercase, and domain meaningful.

### 26.3 Primary

Use `@Primary` only when one implementation is the normal choice and multiple candidates are intentionally present.

### 26.4 Fallback

Use `@Fallback` only for:

- starter defaults;
- offline/local alternatives;
- auto-configuration behavior;
- an implementation that should disappear as soon as a regular candidate exists.

Do not use fallback to conceal ambiguous architecture.

### 26.5 Order

Use `@Order` only when injecting `[]T` or `map[string]T` and order is part of the contract.

Lower values run/inject first. Use meaningful spacing such as `-100`, `0`, `100` so future items can be inserted.

---

## 27. Scope policy

Every managed service, repository, controller, and raw bean MUST declare its scope explicitly.

### 27.1 Singleton

`@Singleton` is required for normal application-owned components even though it is Spice's default.

The redundancy is intentional: lifetime is visible at the declaration.

### 27.2 Prototype

Use `@Prototype` only when a fresh instance is required for each acquisition.

Consumers MUST acquire prototypes through `bean.Provider[T]` so cleanup ownership remains explicit.

### 27.3 Request and session scopes

Use `@RequestScope` and `@SessionScope` only for state that truly belongs to those explicit scopes.

Singletons MUST NOT directly depend on shorter-lived scoped beans. Use the typed provider/scope contract.

### 27.4 Configuration

Generated `@Configuration` values are application configuration providers and are exempt from a redundant scope marker unless the pinned compiler explicitly supports and requires one.

---

## 28. Raw `@Bean` policy

`@Bean` is the principal remaining package-function exception.

Use it only for:

- exact standard-library or third-party types;
- framework types Spice requires exactly;
- external client constructors;
- bridge providers;
- explicit adapter values that cannot be expressed as a constructible owned type;
- the exact marker/provider shape required by a starter.

Do not use `@Bean` for an application-owned struct that can use `@Service`, `@Repository`, `@Controller`, or a custom constructible stereotype.

### 28.1 File shape

A provider file contains exactly one provider.

```go
package platform

import (
    "database/sql"

    "github.com/spice-framework/spice/data"
)

// @import { Bean, Singleton } from "github.com/spice-framework/spice/annotation/core"

// @Bean(name="transactionManager")
// @Singleton
func NewTransactionManagerBean(
    database *sql.DB,
    observers []data.Observer,
) (*data.Manager, error) {
    return data.NewManager(database, observers...)
}
```

Filename:

```text
transaction_manager_bean.go
```

### 28.2 Prefer owned wrappers

When a resource has real lifecycle or application behavior, prefer a named wrapper struct:

```go
type Database struct {
    pool *sql.DB
}
```

The wrapper can own lifecycle, health checks, and domain-safe methods. Do not wrap every trivial value merely for appearance.

---

# Part V — Web, data, lifecycle, events, and background work

## 29. Controller contract

Controller types MUST:

- be exported named structs;
- have an explicit constructor;
- use pointer receivers;
- contain no public mutable fields;
- delegate business behavior;
- use typed request/response DTOs by default.

Preferred route form:

```go
func (controller *OrderController) Create(
    ctx context.Context,
    request CreateOrderRequest,
) (CreateOrderResponse, error)
```

Transactional route form:

```go
func (controller *OrderController) Create(
    ctx context.Context,
    executor data.Executor,
    request CreateOrderRequest,
) (CreateOrderResponse, error)
```

Raw `http.ResponseWriter` / `*http.Request` methods are allowed only when the endpoint must own low-level HTTP semantics such as streaming.

### 29.1 Request DTO

Each request DTO MUST:

- be a named exported struct;
- live in its own file;
- have explicit field binding tags;
- opt out explicitly with `web:"-"`;
- use a value receiver for exact validation when validation is needed.

```go
type CreateOrderRequest struct {
    CustomerID CustomerID `body:"customerId"`
    Quantity   int        `body:"quantity"`
}

func (request CreateOrderRequest) Validate(
    context.Context,
) error {
    if request.Quantity <= 0 {
        return InvalidQuantityError{
            Quantity: request.Quantity,
        }
    }

    return nil
}
```

### 29.2 Route classification

Every route MUST be explicitly classified as one of:

- protected with `@security.Authorize`;
- public through an exact style-policy declaration until Spice provides an explicit permit-all annotation;
- internal/management through the generated management access policy.

A temporary public-route declaration MUST identify the exact package, receiver, and method and MUST include a security-review reason and issue/ADR. Path-only wildcards are forbidden because routes can move or collide.

Unannotated public exposure MUST NOT happen accidentally.

### 29.3 Cache policy

Use `@cache.Cacheable` only for a typed GET that is:

- idempotent;
- authorization-insensitive under the current compiler contract;
- non-transactional;
- safe to key by the validated comparable request;
- safe to cache only on success.

Cache names MUST use stable module/operation identity:

```text
orders.by-id
catalog.product-search
```

Capacity and TTL belong in typed configuration, not annotation arguments.

---

## 30. Transaction policy

Use `@data.Transactional` only at a compiler-supported generated boundary.

Rules:

- transaction ownership remains visible through `data.Executor`;
- an exact `*data.Manager` provider must exist;
- repositories receive an executor explicitly;
- transactions are not hidden in `context.Context`;
- repository methods do not call commit or rollback;
- do not retry a transaction unless the complete operation is idempotent;
- use the weakest correct isolation level;
- use `readOnly=true` only when the driver semantics are understood.

Repository example:

```go
func (repository *PostgresOrderRepository) Save(
    ctx context.Context,
    executor data.Executor,
    order Order,
) error {
    _, err := executor.ExecContext(
        ctx,
        `INSERT INTO orders (id, status) VALUES ($1, $2)`,
        order.ID,
        order.Status,
    )
    return err
}
```

---

## 31. Lifecycle policy

Use `@OnStart` and `@OnStop` for managed components that own an active lifecycle.

Exact shape:

```go
func (component *Component) Start(context.Context) error
func (component *Component) Stop(context.Context) error
```

Rules:

- constructors construct but do not start;
- `@OnStart` starts goroutines/listeners/resources;
- `@OnStop` shuts them down;
- a stop hook requires a start hook under the current compiler contract;
- do not duplicate the same cleanup in both constructor cleanup and `@OnStop`;
- shutdown uses the caller-provided context;
- stop must be safe under repeated application shutdown.

Use constructor `lifecycle.Cleanup` for passive resource closure that does not require a start phase.

---

## 32. Scheduling

Prefer `@schedule.FixedDelay` over a handwritten ticker loop when fixed-delay behavior matches the requirement.

```go
// @FixedDelay(delay="5m", initialDelay="30s", continueOnError=true)
func (service *InventoryService) Refresh(
    ctx context.Context,
) error {
    // ...
}
```

Rules:

- method belongs to one exact managed bean;
- method receives `context.Context`;
- method returns `error`;
- duration strings are explicit;
- overlapping work is not recreated manually;
- cancellation is honored;
- errors are returned rather than swallowed.

---

## 33. Asynchronous execution

Prefer `@async.Execute` over a hidden goroutine or global worker pool.

```go
// @Execute
func (mailer *OrderMailer) SendConfirmation(
    ctx context.Context,
    message OrderConfirmation,
) error {
    // ...
}
```

Rules:

- asynchronous work is a method on a managed struct;
- context is first;
- error is returned;
- arguments are explicit typed values;
- no caller assumes completion unless it receives an explicit completion contract;
- concurrency limits remain typed generated configuration.

---

## 34. Typed events

Events are exported named values and live one per file.

Use past-tense names:

```text
OrderCreated
PaymentAuthorized
ShipmentDispatched
```

Listeners are methods on managed structs:

```go
// @Listener(order=100)
func (projection *OrderProjection) OnOrderCreated(
    ctx context.Context,
    event OrderCreated,
) error {
    // ...
}
```

Topic declarations use one marker-only `*_topic.go` file because the current official descriptor targets a package-level function.

Rules:

- no global event bus;
- publication is instance-owned;
- payload types are exact;
- listener order is explicit only when semantically required;
- events do not carry infrastructure objects;
- cross-module events belong in an explicitly exposed named interface package.

---

## 35. Security

Protected routes use `@security.Authorize`.

Prefer simple explicit declarations:

```go
// @Authorize(authenticated=true, anyRoles=["operator", "admin"], allScopes=["orders.write"])
```

Use `expression` only when roles/scopes cannot express the policy clearly.

Restricted expressions MUST remain:

- side-effect free;
- small;
- reviewable;
- independent of bean lookup;
- independent of reflective property navigation;
- free of I/O.

Management endpoints SHOULD use:

```go
@Enable(
    expose=["health", "liveness", "readiness", "info", "metrics", "modules"],
    access="loopback",
)
```

Expose `configprops` only when its operational value is understood. Secrets remain tagged and redacted.

---

## 36. Observability

Use `@observability.Logging` at the application boundary.

Application components MUST NOT install or mutate a global logger.

Inject an explicit logger or observer seam when component-level logging is required.

Structured logs MUST:

- use stable static messages;
- use snake-case keys;
- carry context where supported;
- avoid secrets and raw rejected input;
- identify component, operation, and phase;
- avoid duplicate logging at every layer.

---

# Part VI — General Go rules

## 37. Context

`context.Context` MUST:

- be the first parameter;
- never be stored in a struct;
- never be replaced with `context.Background()` inside request/application work;
- be propagated to I/O;
- be honored for cancellation;
- not carry dependencies or mutable business state.

Constructors SHOULD accept context only when construction performs cancellable work. Prefer lifecycle startup for active work.

---

## 38. Function and method signatures

Rules:

- context first;
- error last;
- no unnamed Boolean mode flags;
- no more than five ordinary parameters without introducing a request/options type;
- no variadic application APIs unless the semantic is truly a sequence;
- avoid naked multiple primitive results;
- return a named result type when output has domain meaning;
- named return variables SHOULD be avoided except for short cleanup/defer logic.

Forbidden:

```go
func (service *OrderService) Create(
    ctx context.Context,
    id string,
    qty int,
    dryRun bool,
    skipValidation bool,
    notify bool,
) (string, bool, error)
```

Preferred:

```go
type CreateOrderCommand struct {
    CustomerID CustomerID
    Quantity   int
    Mode       CreateOrderMode
}

func (service *OrderService) Create(
    ctx context.Context,
    command CreateOrderCommand,
) (OrderView, error)
```

---

## 39. Control flow and complexity

Prefer:

- early returns;
- small methods;
- explicit switch statements;
- exhaustive enum handling;
- straightforward loops;
- typed intermediate values.

Avoid:

- deep nesting;
- clever one-liners;
- Boolean state machines;
- long anonymous callbacks;
- mutation spread across several layers;
- generic abstractions before repeated need exists.

Automated targets:

- cyclomatic complexity MUST be at most 15 per function;
- package average SHOULD remain at most 10;
- maintainability index SHOULD remain at least 20.

---

## 40. Collections and ownership

Rules:

- return copies when exposing mutable internal slices/maps;
- do not retain caller-owned mutable slices/maps without documenting ownership;
- use nil slices when empty unless wire semantics require `[]`;
- avoid maps as untyped option bags;
- do not use `map[string]any` for application contracts;
- use typed `[]T` and `map[string]T` for Spice collection injection;
- apply `@Order` only when order is part of the bean contract.

---

## 41. Concurrency

Goroutine ownership MUST be visible.

Every goroutine must have:

- an owning struct;
- a start boundary;
- cancellation;
- a stop/join path;
- panic containment where appropriate;
- bounded concurrency;
- deterministic test coverage.

Prefer Spice lifecycle, scheduling, and async generation over manual concurrency.

Forbidden:

```go
func NewWorker() *Worker {
    worker := &Worker{}
    go worker.loop()
    return worker
}
```

---

## 42. Standard formatting and documentation

All source MUST pass:

```text
gofumpt
goimports
gofmt
```

Every exported package, type, function, method, field with non-obvious semantics, constant, and variable MUST have GoDoc.

Documentation MUST explain:

- responsibility;
- invariants;
- lifecycle ownership;
- security-sensitive behavior;
- non-obvious error behavior;
- intentional exceptions.

Comments MUST explain why, not narrate obvious syntax.

---

# Part VII — Testing

## 43. Test layout

Production type:

```text
order_service.go
```

Primary unit test:

```text
order_service_test.go
```

Rules:

- test files may contain Go-required package functions;
- use external package tests (`orders_test`) for public behavior where practical;
- use same-package tests only for important internal contracts;
- do not create a second production type in a test file;
- named test harness structs are allowed and follow one-type-per-file within test source;
- table tests remain acceptable;
- integration tests belong under an explicit integration package or build tag;
- acceptance tests live outside generated directories.

---

## 44. Spice test facilities

Use generated typed seams rather than a mutable test container.

Preferred facilities:

- `spicetest.NewContext`;
- generated `Components`;
- generated `BeanOverrides`;
- generated `BeanOverrideLayer`;
- `bean.Replace`;
- `bean.ReplaceFactory`;
- `spicetest.NewHTTP`;
- `spicetest.NewSQL`;
- `spice test --module`.

Rules:

- overrides are exact typed values;
- no string bean lookup;
- no mutation of a running graph;
- shutdown is always registered with `t.Cleanup`;
- test configuration uses explicit `config.Source`;
- SQL slice tests use the supplied transaction-owned `data.Executor`;
- module tests select the actual module import path.

---

## 45. Coverage and test quality

The project MUST:

- run tests under the race detector in CI;
- shuffle broad test execution where supported;
- maintain at least 85% aggregate business-source coverage;
- test error and cancellation paths;
- test lifecycle rollback;
- test generated freshness;
- test module boundaries;
- test public route security classification;
- test enum invalid/zero values;
- fuzz parsers, decoders, and protocol boundaries where useful.

Coverage does not excuse poor assertions or tests coupled to implementation details.

---

# Part VIII — Quality gate

## 46. Required verification order

The CI gate SHOULD run in this order:

1. module/tidy/vendor consistency;
2. formatting;
3. `spicestyle`;
4. `spice verify`;
5. module graph verification;
6. generated freshness;
7. `go vet`;
8. allowlisted `golangci-lint`;
9. nil-flow analysis;
10. security and vulnerability analysis;
11. race-enabled tests and coverage;
12. focused fuzz smoke;
13. offline build/test;
14. executable application smoke checks.

Current Spice commands include:

```bash
go tool github.com/spice-framework/toolchain/cmd/spice verify ./...

go tool github.com/spice-framework/toolchain/cmd/spice \
    generate --check --target Shop ./cmd/shop

go tool github.com/spice-framework/toolchain/cmd/spice \
    modules --format=json ./cmd/shop

go tool github.com/spice-framework/toolchain/cmd/spice \
    test --module=example.com/shop/internal/orders \
    --race \
    --count=1 \
    ./...
```

The reviewed Toolchain baseline includes the first typed compiler profile. The
standalone `spicestyle` structural command and strict schema-one configuration
specified below are the executable enforcement surface completed by this
adoption work.

---

## 47. Linter baseline

Start from Spice's allowlist rather than enabling every stylistic linter.

Required categories include:

- correctness;
- error handling;
- nil flow;
- contexts;
- security;
- SQL resource handling;
- documentation;
- maintainability;
- architecture;
- exhaustive enum handling;
- suppression discipline;
- structured logging;
- unused/dead code.

Recommended enabled linters include:

```text
asciicheck
bidichk
bodyclose
containedctx
cyclop
depguard
durationcheck
errcheck
errorlint
exhaustive
fatcontext
forbidigo
forcetypeassert
gocheckcompilerdirectives
gocritic
godoclint
gomoddirectives
govet
ineffassign
interfacebloat
maintidx
modernize
nilerr
nilnesserr
nilnil
noctx
nolintlint
nosprintfhostport
predeclared
recvcheck
revive
rowserrcheck
sloglint
sqlclosecheck
staticcheck
testableexamples
thelper
tparallel
unconvert
unparam
unused
usestdlibvars
wastedassign
```

Do not adopt noisy rules merely to maximize linter count. In particular, line length, variable-name length, magic-number, function-length, blanket wrapping, and forced sentinel-error policies SHOULD remain review-guided unless the project has a precise need.

---

## 48. Forbidden APIs

At minimum, application linting MUST forbid:

```text
fmt.Print*
log.Fatal*
os.Exit outside package-main entrypoint
slog.Default or global logger mutation
http.DefaultClient for production integrations
context.Background inside request work
time.Tick
unsafe unless approved
reflect-based dependency injection
```

Use injected writers/loggers/clients and return errors to the process boundary.

---

# Part IX — Automated enforcement implementation

## 49. Enforcement architecture

The style must be executable, not aspirational.

The `spicestyle` verifier belongs in the separately versioned Spice toolchain rather than the Spice runtime/core package.

Recommended layout:

```text
spice-framework/toolchain/
├── cmd/
│   └── spicestyle/
│       └── main.go
└── internal/
    └── style/
        ├── analyzer.go
        ├── configuration.go
        ├── diagnostics.go
        ├── files.go
        ├── functions.go
        ├── methods.go
        ├── types.go
        ├── annotations.go
        ├── providers.go
        ├── modules.go
        ├── suppressions.go
        └── testdata/
```

The full implementation SHOULD have two layers.

### 49.1 Structural Go analyzer

Use `golang.org/x/tools/go/analysis` with:

- AST;
- `go/types`;
- `inspect.Analyzer`;
- build-selected files only;
- standard generated-file detection;
- exact source positions;
- deterministic diagnostics.

This layer enforces file/type/function/receiver/global-state/signature rules.

### 49.2 Spice-aware compiler phase

Run after Spice's existing:

- package load;
- annotation resolution;
- descriptor handler contributions;
- provider catalog;
- module graph.

This layer MUST inspect typed contribution kinds, not match annotation names as strings.

It enforces:

- stereotype use;
- explicit constructor selection;
- explicit scope;
- explicit interface bindings;
- raw provider exceptions;
- module ownership;
- route security classification;
- annotation ordering/import policy;
- supported annotation combinations.

The same phase SHOULD feed:

- `spicestyle`;
- `spice verify`;
- `spice lsp`;
- the GoLand integration;
- CI JSON diagnostics.

---

## 50. Command contract

The target command surface is:

```bash
go tool github.com/spice-framework/toolchain/cmd/spicestyle ./...

go tool github.com/spice-framework/toolchain/cmd/spicestyle \
    --config=.spice/style.json \
    --format=text \
    ./...
```

Machine-readable diagnostics:

```bash
go tool github.com/spice-framework/toolchain/cmd/spicestyle \
    --config=.spice/style.json \
    --format=json \
    ./...
```

After integration into the shared compiler service:

```bash
go tool github.com/spice-framework/toolchain/cmd/spice verify \
    --style=.spice/style.json \
    ./...
```

Run the standalone structural analyzer beside `spice verify`; the latter owns
the typed Spice-aware phase. Both commands use the same `java-structured`
profile identity and stable diagnostic namespace.

---

## 51. Style configuration

Use JSON to match Spice's existing manifest-oriented tooling and avoid configuration ambiguity.

```json
{
  "schemaVersion": 1,
  "profile": "java-structured",
  "sourceRoots": [
    "cmd",
    "internal"
  ],
  "generatedRoots": [
    "internal/spicegen"
  ],
  "rules": {
    "onePrimaryTypePerFile": "error",
    "methodsInPrimaryTypeFile": "error",
    "fileNameMatchesType": "error",
    "packageFunctions": "error",
    "explicitConstructors": "error",
    "explicitManagedScopes": "error",
    "banInit": "error",
    "banMutablePackageState": "error",
    "privateManagedFields": "error",
    "moduleOwnership": "error",
    "routeClassification": "error",
    "contextFirst": "error",
    "errorLast": "error",
    "maxTypeFileLines": 500
  },
  "publicRoutes": [
    {
      "package": "example.com/shop/internal/catalog",
      "receiver": "CatalogController",
      "method": "List",
      "reason": "Anonymous product browsing is a product requirement",
      "issue": "SEC-123"
    }
  ],
  "allowedBoundaryFiles": [
    "**/doc.go",
    "**/main.go",
    "**/*_bean.go",
    "**/*_topic.go",
    "**/*_test.go",
    "internal/spicegen/**"
  ],
  "packageFunctionExceptions": [
    {
      "glob": "**/main.go",
      "symbol": "main",
      "reason": "Go process entrypoint"
    },
    {
      "glob": "**/*_bean.go",
      "contributionKind": "provider",
      "maximum": 1,
      "reason": "Exact Spice provider boundary"
    },
    {
      "glob": "**/*_topic.go",
      "contributionKind": "event-topic",
      "maximum": 1,
      "reason": "Typed Spice event topic marker"
    },
    {
      "glob": "**/*_test.go",
      "symbolPattern": "^(Test|Benchmark|Fuzz|Example|TestMain)",
      "reason": "Go testing entrypoint"
    }
  ],
  "packageVariableExceptions": [
    {
      "glob": "internal/presentation/assets.go",
      "symbol": "files",
      "type": "embed.FS",
      "reason": "Go embed requires a package variable",
      "issue": "ARCH-123"
    }
  ]
}
```

The configuration MUST fail on unknown fields and unsupported schema versions.
Package-variable exceptions are exact interoperability boundaries: file glob,
symbol, and resolved Go type must all match, and both a reason and issue/ADR
identifier are mandatory. Symbol patterns, file-wide variable exceptions, and
type wildcards are forbidden.

---

## 52. Diagnostic codes

Use stable namespaced codes.

| Code | Meaning |
|---|---|
| `spice.style.file.one-primary-type` | File has zero/multiple types without a boundary exception |
| `spice.style.file.name` | Filename does not match primary type |
| `spice.style.file.method-owner` | Method is not in receiver type file |
| `spice.style.file.unrelated-declaration` | Declaration is unrelated to primary type |
| `spice.style.function.package-level` | Unapproved free function |
| `spice.style.function.init` | Handwritten `init` function |
| `spice.style.constructor.name` | Invalid constructor/factory name |
| `spice.style.constructor.location` | Constructor not in type file |
| `spice.style.constructor.explicit` | Managed type relies on inferred/generated constructor |
| `spice.style.bean.stereotype` | Owned struct uses raw `@Bean` or wrong stereotype |
| `spice.style.bean.scope` | Managed bean has no explicit scope |
| `spice.style.bean.interface-binding` | Interface injection lacks explicit binding |
| `spice.style.bean.fields-private` | Managed component exposes mutable fields |
| `spice.style.package.mutable-global` | Mutable package variable |
| `spice.style.package.module` | Package lacks/duplicates module ownership |
| `spice.style.module.dependency` | Undeclared or invalid module edge |
| `spice.style.annotation.import` | Missing/noncanonical annotation import |
| `spice.style.annotation.order` | Annotation order differs from policy |
| `spice.style.route.classification` | Route is neither protected nor explicitly public |
| `spice.style.context.first` | Context is not first |
| `spice.style.context.stored` | Context stored in struct |
| `spice.style.error.last` | Error is not final result |
| `spice.style.receiver.name` | Receiver name inconsistent/non-descriptive |
| `spice.style.type.role-name` | Discouraged role name such as `Impl` or `Utils` |
| `spice.style.suppression.invalid` | Missing reason, issue, exact code, or unused suppression |

Diagnostics MUST include exact source ranges and safe related information.

---

## 53. Structural analyzer algorithm

### 53.1 Generated files

Skip files when either is true:

- standard `// Code generated ... DO NOT EDIT.` header;
- path is under an exact configured generated root.

Do not skip a file merely because its name contains `gen`.

### 53.2 Count primary types

For every selected handwritten file:

1. collect all `ast.TypeSpec` declarations;
2. count aliases and definitions;
3. reject grouped multi-type declarations;
4. identify the sole type;
5. apply the filename transform;
6. classify allowed companions.

A normal source file with no primary type fails unless it matches a recognized boundary category.

### 53.3 Validate methods

For each `ast.FuncDecl` with a receiver:

1. resolve the receiver's base named type through `go/types`;
2. find the physical source file of the type declaration;
3. require the method file to match;
4. verify receiver-name consistency;
5. verify context/error conventions where relevant.

### 53.4 Validate package functions

For each `ast.FuncDecl` without a receiver:

1. reject `init`;
2. allow exact `main` only in the command boundary;
3. allow a constructor/factory only when its name and first result associate it with the file's primary type;
4. query resolved Spice contribution metadata for `provider` or `event-topic`;
5. apply test/tool exceptions;
6. reject every other function.

Do not allow by broad filename alone. A `*_bean.go` file must contain exactly one resolved provider contribution.

### 53.5 Validate package state

For each package-level `var`:

- reject by default;
- allow generated code;
- allow an exact approved compile assertion only when Spice cannot generate it;
- require a typed style exception for any interoperability case.

For each package-level `const`:

- require it to be associated with the primary type or an approved protocol-constant boundary.

### 53.6 Spice-aware validation

Use resolved typed contributions to determine:

- constructible stereotype;
- provider;
- scope;
- interface bindings;
- route;
- authorization;
- transaction;
- cache;
- lifecycle;
- scheduling;
- async;
- module ownership.

Never decide that a function is a bean merely because its comment contains the text `@Bean`.

---

## 54. Suppression policy

Suppressions are exceptional and auditable. Schema-one enforcement supports
only the exact configuration-owned function and variable exceptions above.
The following declaration-local syntax is reserved for a later schema and is
not accepted by current tooling:

```go
//spicestyle:allow spice.style.function.package-level issue=ARCH-123
// reason: grpc-go requires a package-level registration callback.
func Register...
```

Rules:

- one exact diagnostic code;
- a non-empty reason;
- an issue/ADR identifier;
- declaration-local scope;
- no file-wide wildcard;
- no `all`;
- unused suppressions fail;
- expired suppressions fail when an expiration is supplied;
- generated and standard boundary exceptions do not need suppressions.

CI SHOULD report the complete active-suppression inventory.

---

## 55. Safe fixes

The LSP MAY offer safe fixes for:

- canonical annotation spacing;
- adding a missing annotation import;
- sorting annotation imports;
- sorting annotations;
- adding explicit `constructor=New<Type>`;
- adding explicit `@Singleton`;
- receiver-name normalization;
- filename rename when unambiguous.

The tool MUST NOT automatically:

- move business logic to a receiver;
- split a multi-type file without review;
- invent a struct;
- choose a module boundary;
- choose a qualifier;
- change bean scope;
- classify a route as public;
- rewrite transaction ownership.

Those require architectural intent.

---

# Part X — Spice abstractions to add

## 56. Available today through the annotation SDK

Spice's annotation SDK can define third-party descriptors that contribute the same typed compiler capabilities as official descriptors.

A project MAY add a small versioned annotation module for semantic stereotypes that are missing from the official set. The generic `@Component` shown below is now official; its example remains the baseline for a constructible support component.

### 56.1 `@Component`

Official descriptor:

```go
// @Component(constructor=NewClock)
type SystemClock struct{}
```

It contributes:

- constructible stereotype role `component`;
- optional constructor;
- optional bean name/aliases;
- singleton scope by project policy or a separate explicit scope annotation.

Use it for infrastructure/support components that are not honestly services, repositories, or controllers.

### 56.2 `@UseCase`

Optional semantic stereotype:

```go
// @UseCase(constructor=NewCreateOrder)
type CreateOrder struct {
    // ...
}
```

It may contribute a constructible service stereotype while retaining an explicit type role in diagrams and diagnostics.

Do not create many nearly identical stereotypes. Each must have a durable architectural meaning.

### 56.3 `@Adapter`

Optional semantic stereotype for external adapters:

```go
// @Adapter(constructor=NewStripeGateway)
type StripeGateway struct {
    // ...
}
```

Continue to use official `@Implements` for exact interface bindings.

### 56.4 Annotation-extension rules

Custom descriptors MUST:

- live in a versioned Go module;
- have one descriptor and handler per file;
- use static `sdk.Definition` metadata;
- use typed contribution kinds;
- declare compatibility;
- include examples and GoDoc;
- run through an authorized Go tool;
- have deterministic handler tests;
- never execute application code during analysis;
- not introduce name-based compiler switches.

---

## 57. Framework enhancements required for stronger Java parity

These require Spice compiler/framework work rather than style policy alone.

### 57.1 Method-level bean providers and `@Factory`

**Priority: P0**

Current `@Bean` providers are package-level functions. This is the largest remaining obstacle to a Java-class-oriented source model.

Add:

```go
// @Factory(constructor=NewInfrastructureConfiguration)
// @Singleton
type InfrastructureConfiguration struct {
    // dependencies
}

// @Bean(name="database")
func (configuration *InfrastructureConfiguration) Database(
    ctx context.Context,
) (*sql.DB, lifecycle.Cleanup, error) {
    // ...
}
```

Required compiler semantics:

- `@Factory` is a constructible type stereotype;
- `@Bean` may target an exported method owned by one exact factory bean;
- factory receiver becomes an explicit dependency edge;
- method parameters remain exact dependency edges;
- method result shapes match package-level providers;
- cleanup ownership remains unchanged;
- generated code calls the concrete method directly;
- provider cycles remain compile errors;
- no reflection or proxy is added;
- stable provider identity includes factory receiver and method;
- method-provider files remain the factory's type file under this profile.

This feature would eliminate nearly every remaining unassociated provider function.

### 57.2 Official generic `@Component` — delivered

The reviewed baseline includes an official constructible `@Component` with:

```text
constructor
name
aliases
```

This prevents misuse of `@Service` for infrastructure components and removes the need for every project to maintain the same custom descriptor. The style verifier must keep this behavior locked as an available contract rather than treating it as future work.

### 57.3 Explicit public-route annotation

**Priority: P1**

Add an explicit secure declaration such as:

```go
// @PermitAll
```

or:

```go
// @Authorize(public=true)
```

The compiler should require every generated route to be exactly one of:

- protected;
- explicitly public;
- generated management/internal.

This removes policy-file exceptions for public routes.

### 57.4 General method transaction boundaries

**Priority: P1**

Current practical transaction generation is centered on supported HTTP routes. Add an explicit generated service-method boundary that:

- preserves typed `data.Executor`;
- generates a visible direct wrapper;
- avoids proxy/self-invocation traps;
- rejects internal self-calls that bypass the wrapper;
- owns stable operation/module identity.

This would align transactions with application-service use cases while remaining more explicit than Spring proxies.

### 57.5 Style profile in `spice verify`

**Priority: P1**

Integrate the analyzer and `.spice/style.json` into the compiler service so CLI, LSP, GoLand, CI, and generation share one result.

### 57.6 Composed project stereotypes

**Priority: P2**

Add first-class composition only if it preserves typed static contributions and clear navigation.

Do not add opaque annotation stacks that make behavior harder to discover.

---

# Part XI — Reference examples

## 58. Service example

```go
package orders

import (
    "context"
)

// @import { Implements, Service, Singleton } from "github.com/spice-framework/spice/annotation/core"
// @import * as ordersapi from "example.com/shop/internal/orders/api"

// OrderService executes order use cases.
//
// @Service(constructor=NewOrderService)
// @Singleton
// @Implements(ordersapi.OrderCreator)
type OrderService struct {
    repository OrderRepository
}

func NewOrderService(
    repository OrderRepository,
) (*OrderService, error) {
    if repository == nil {
        return nil, MissingOrderRepositoryError{}
    }

    return &OrderService{
        repository: repository,
    }, nil
}

func (service *OrderService) Create(
    ctx context.Context,
    command ordersapi.CreateOrderCommand,
) (ordersapi.OrderView, error) {
    // ...
}
```

Notes:

- one primary type;
- constructor in same file;
- no package helper;
- private dependency;
- explicit stereotype;
- explicit singleton;
- explicit interface binding.

---

## 59. Repository interface example

```go
package orders

import (
    "context"

    "github.com/spice-framework/spice/data"
)

// OrderRepository stores and retrieves orders.
type OrderRepository interface {
    Save(
        context.Context,
        data.Executor,
        Order,
    ) error

    Find(
        context.Context,
        data.Executor,
        OrderID,
    ) (Order, error)
}
```

One interface, one file.

---

## 60. Repository implementation example

```go
package orders

import (
    "context"

    "github.com/spice-framework/spice/data"
)

// @import { Implements, Repository, Singleton } from "github.com/spice-framework/spice/annotation/core"

// PostgresOrderRepository stores orders through database/sql.
//
// @Repository(constructor=NewPostgresOrderRepository)
// @Singleton
// @Implements(OrderRepository)
type PostgresOrderRepository struct {
    insertStatement string
}

func NewPostgresOrderRepository() *PostgresOrderRepository {
    return &PostgresOrderRepository{
        insertStatement: `
            INSERT INTO orders (id, status)
            VALUES ($1, $2)
        `,
    }
}

func (repository *PostgresOrderRepository) Save(
    ctx context.Context,
    executor data.Executor,
    order Order,
) error {
    _, err := executor.ExecContext(
        ctx,
        repository.insertStatement,
        order.ID,
        order.Status,
    )
    return err
}
```

In the same package, `@Implements(OrderRepository)` may use an appropriate resolvable type expression under the pinned annotation-import rules; a dedicated named-interface package is preferable for cross-module contracts.

---

## 61. Controller example

```go
package orders

import (
    "context"

    "github.com/spice-framework/spice/data"
)

// @import { Singleton } from "github.com/spice-framework/spice/annotation/core"
// @import { Transactional } from "github.com/spice-framework/spice/annotation/data"
// @import { Authorize } from "github.com/spice-framework/spice/annotation/security"
// @import { Controller, Post } from "github.com/spice-framework/spice/annotation/web"

// OrderController exposes order HTTP operations.
//
// @Controller(prefix="/orders", constructor=NewOrderController)
// @Singleton
type OrderController struct {
    service *OrderService
}

func NewOrderController(
    service *OrderService,
) (*OrderController, error) {
    if service == nil {
        return nil, MissingOrderServiceError{}
    }

    return &OrderController{
        service: service,
    }, nil
}

// @Post("/")
// @Authorize(authenticated=true, allScopes=["orders.write"])
// @Transactional(isolation="serializable")
func (controller *OrderController) Create(
    ctx context.Context,
    executor data.Executor,
    request CreateOrderRequest,
) (CreateOrderResponse, error) {
    // Map the request to a typed command and delegate.
    // ...
}
```

---

## 62. Enum example

```go
package orders

// OrderStatus is the persisted lifecycle state of an order.
type OrderStatus string

const (
    OrderStatusUnknown   OrderStatus = ""
    OrderStatusPending   OrderStatus = "pending"
    OrderStatusConfirmed OrderStatus = "confirmed"
    OrderStatusCancelled OrderStatus = "cancelled"
)

func (status OrderStatus) Valid() bool {
    switch status {
    case OrderStatusPending,
        OrderStatusConfirmed,
        OrderStatusCancelled:
        return true
    default:
        return false
    }
}
```

---

## 63. Lifecycle resource example

```go
package platform

import (
    "context"
)

// @import { Service, Singleton } from "github.com/spice-framework/spice/annotation/core"
// @import { OnStart, OnStop } from "github.com/spice-framework/spice/annotation/lifecycle"

// HTTPServer owns the application's HTTP listener lifecycle.
//
// @Service(constructor=NewHTTPServer)
// @Singleton
type HTTPServer struct {
    // ...
}

func NewHTTPServer(
    configuration ServerConfiguration,
) (*HTTPServer, error) {
    // Construct, but do not start listening.
    // ...
}

// @OnStart
func (server *HTTPServer) Start(
    ctx context.Context,
) error {
    // ...
}

// @OnStop
func (server *HTTPServer) Stop(
    ctx context.Context,
) error {
    // ...
}
```

---

# Part XII — Adoption plan

## 64. Implementation sequence

Adopt the profile through small mechanically verifiable changes.

### Step 1 — Pin the framework contract

- pin compatible Spice core and toolchain versions;
- record the reviewed descriptor baseline;
- run current `spice verify`;
- resolve existing annotation/compiler errors before style migration.

### Step 2 — Inventory the repository

Produce a machine-readable report of:

- files with multiple named types;
- files with no primary type;
- package-level functions;
- package variables;
- `init` functions;
- methods separated from receiver files;
- current stereotypes;
- raw bean providers;
- inferred constructors;
- implicit/default scopes;
- interface injections;
- module ownership;
- generated-file freshness.

Do not refactor blindly before this inventory exists.

### Step 3 — Establish module boundaries

- add or correct `@Module` package documentation;
- define exact `allowedDependencies`;
- create named interfaces only for deliberate APIs;
- eliminate cycles and unassigned packages;
- move toward package-by-feature organization.

Run the module graph after every boundary change.

### Step 4 — Split files

- move every named type into its own file;
- move all methods to the receiver type file;
- move constructors to the type file;
- separate enums, DTOs, interfaces, errors, and configuration;
- rename files to canonical snake case.

This step should not change behavior.

### Step 5 — Eliminate loose behavior

Classify every package function as:

- constructor/factory;
- application entrypoint;
- raw Spice provider;
- event-topic marker;
- test/tool boundary;
- behavior that must become a method;
- generic language exception.

Convert unclassified functions to receiver methods or real collaborating types.

### Step 6 — Normalize managed components

- replace raw `@Bean` providers for owned structs with stereotypes;
- add explicit `constructor=New<Type>`;
- add explicit scope;
- make fields private;
- remove setter/global injection;
- add `@Implements` for interfaces;
- replace accidental ambiguity with qualifiers or better type design.

### Step 7 — Normalize framework boundaries

- typed configuration instead of direct environment access;
- typed controller DTOs;
- explicit route security classification;
- explicit transaction executors;
- generated scheduling/async where appropriate;
- typed events instead of global buses;
- lifecycle methods instead of constructor goroutines.

### Step 8 — Introduce `spicestyle` in report mode

- emit all diagnostics;
- create the initial exception inventory;
- require reasons and issue IDs;
- do not add new violations.

### Step 9 — Fail changed-code violations

- fail new or modified files first;
- keep the baseline visible;
- burn down baseline exceptions by module;
- reject broad suppressions.

### Step 10 — Enforce repository-wide

- remove baseline mode;
- fail all violations;
- integrate with `spice verify`, LSP, GoLand, and CI;
- publish style diagnostics as artifacts;
- require generated freshness, module verification, race tests, coverage, security, and offline checks.

### Step 11 — Add missing Spice abstractions

Implement in this order:

1. method-level `@Bean` plus `@Factory`;
2. explicit public-route annotation;
3. general service-method transactions;
4. first-class `java-structured` style configuration and enforcement.

---

# Part XIII — Review checklist

## 65. Pull request checklist

A change is ready only when all applicable answers are yes.

### Structure

- [ ] Every handwritten production file has exactly one primary type or a documented boundary exception.
- [ ] The filename matches the primary type.
- [ ] All methods are in the primary type's file.
- [ ] Constructors are in the type file.
- [ ] No unrelated declarations share a file.
- [ ] No new package mutable state or `init` function exists.

### Spice

- [ ] Every annotation resolves through an import in the same file.
- [ ] The most specific stereotype is used.
- [ ] Every managed type has an explicit constructor.
- [ ] Every managed bean has an explicit scope.
- [ ] Every interface exposure uses `@Implements` or an exact interface provider.
- [ ] Raw `@Bean` is used only for an approved boundary.
- [ ] Bean names, qualifiers, primary/fallback, and order are intentional.
- [ ] Module ownership and allowed dependencies are correct.
- [ ] Generated source is fresh and untouched.

### Behavior

- [ ] Business behavior belongs to a named type.
- [ ] Controllers do not own business rules.
- [ ] Repositories do not own transaction commit/rollback.
- [ ] Context is first and not stored.
- [ ] Errors are last, safe, and preserved.
- [ ] Concurrency has visible ownership and shutdown.
- [ ] Public routes are explicitly classified.
- [ ] Secrets are typed and redacted.

### Quality

- [ ] Formatting passes.
- [ ] `spicestyle` passes.
- [ ] `spice verify` passes.
- [ ] Module verification passes.
- [ ] Generation check passes.
- [ ] Vet/lint/nil/security checks pass.
- [ ] Race-enabled tests pass.
- [ ] Coverage remains at or above the project floor.
- [ ] Offline verification passes where required.

---

# Part XIV — Final decision rules

When uncertain, apply these rules in order:

1. **Can the behavior naturally belong to an existing domain or application type?**
   Make it a method.

2. **Is it construction of the file's primary type?**
   Use `New<Type>` in that file.

3. **Is it a managed application-owned component?**
   Use the most specific constructible Spice stereotype with explicit constructor and scope.

4. **Is it an exact external/framework type?**
   Use one approved `@Bean` boundary or a meaningful owned wrapper.

5. **Is it a typed event-topic marker or Go-required entrypoint?**
   Use the exact dedicated boundary-file exception.

6. **Is Go's generic/function model the only reason it cannot be a method?**
   Use a narrow package, pure function, tests, and a documented exception.

7. **Does the file need a second named type or the type need another method file?**
   Split the responsibility, not the class-like source unit.

The desired codebase should read like a collection of small explicit classes, interfaces, enums, records, and package/module descriptors—while still compiling, debugging, testing, and deploying as ordinary direct Go.
