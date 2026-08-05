package provider

import (
	"context"
	"fmt"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestConstructibleStereotypeBindsExplicitInterfaces(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/interfacebeans\n\ngo 1.26.0\n",
		"api/api.go": `package api

type Processor interface { Process() error }
type Repository[T any] interface { Save(T) error }
`,
		"app/app.go": `package app

import "example.com/interfacebeans/api"

type Order struct{}
type Config struct{}

// @Bean
func NewConfig() Config { return Config{} }

// @Service
// @Implements(api.Processor, api.Repository[Order])
type Stripe struct{ config Config }

var _ api.Processor = (*Stripe)(nil)
var _ api.Repository[Order] = (*Stripe)(nil)

func NewStripe(config Config) *Stripe { return &Stripe{config: config} }
func (*Stripe) Process() error { return nil }
func (*Stripe) Save(Order) error { return nil }

type Checkout struct{}

// @Bean
func NewCheckout(
	processor api.Processor,
	repository api.Repository[Order],
) *Checkout {
	return &Checkout{}
}
`,
	})
	program, resolved := loadAndResolve(t, root, "./...")
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf(
			"Build() diagnostics = %v",
			diagnosticStrings(diagnostics),
		)
	}
	stripe := providerByName(catalog.Providers(), "stripe")
	if stripe == nil {
		t.Fatalf("stereotype provider missing: %#v", catalog.Providers())
	}
	if stripe.Source != SourceStereotype ||
		stripe.Construction != ConstructionFactory ||
		stripe.Constructor.Name != "NewStripe" ||
		stripe.OutputTypeID != "*example.com/interfacebeans/app.Stripe" ||
		len(stripe.Interfaces) != 2 {
		t.Fatalf("stereotype provider = %#v", stripe)
	}
	if stripe.Interfaces[0].TypeID !=
		"example.com/interfacebeans/api.Processor" ||
		stripe.Interfaces[1].TypeID !=
			"example.com/interfacebeans/api.Repository[example.com/interfacebeans/app.Order]" {
		t.Fatalf("interface bindings = %#v", stripe.Interfaces)
	}
	copyOne := catalog.Providers()
	copyOne[0].Interfaces = append(
		copyOne[0].Interfaces,
		InterfaceBinding{TypeID: "mutated"},
	)
	if len(catalog.Providers()[0].Interfaces) ==
		len(copyOne[0].Interfaces) {
		t.Fatal("Providers() exposed interface binding storage")
	}
}

func TestBeanMetadataAttachesToBeansAndExactConstructorParameters(
	t *testing.T,
) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/selection\n\ngo 1.26.0\n",
		"selection.go": `package selection

type Processor struct{}
type Checkout struct{ processor Processor }

// @Bean(name="stripe", aliases=["card"])
// @Qualifier("payments")
// @Primary
// @Order(-10)
// @Prototype
func StripeProcessor() Processor { return Processor{} }

// @Bean(name="offline")
// @Fallback
func OfflineProcessor() Processor { return Processor{} }

// @Bean
func NewCheckout(
	// @Qualifier("card")
	processor Processor,
) *Checkout {
	return &Checkout{processor: processor}
}
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	resolved = testannotation.Trust(resolved)
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnosticStrings(diagnostics))
	}
	stripe := providerByName(catalog.Providers(), "stripe")
	if stripe == nil ||
		len(stripe.Aliases) != 1 ||
		stripe.Aliases[0] != "card" ||
		len(stripe.Qualifiers) != 1 ||
		stripe.Qualifiers[0] != "payments" ||
		!stripe.Primary ||
		stripe.Fallback ||
		stripe.Order != -10 ||
		stripe.Scope != sdk.BeanScopePrototype {
		t.Fatalf("stripe metadata = %#v", stripe)
	}
	offline := providerByName(catalog.Providers(), "offline")
	if offline == nil || !offline.Fallback ||
		offline.Scope != sdk.BeanScopeSingleton {
		t.Fatalf("offline metadata = %#v", offline)
	}
	checkout := providerByName(
		catalog.Providers(),
		"newCheckout",
	)
	if checkout == nil ||
		len(checkout.Dependencies) != 1 ||
		len(checkout.Dependencies[0].Qualifiers) != 1 ||
		checkout.Dependencies[0].Qualifiers[0] != "card" ||
		checkout.Dependencies[0].Position.Line == 0 {
		t.Fatalf("checkout dependency = %#v", checkout)
	}

	copyOne := catalog.Providers()
	copyOne[0].Aliases = append(copyOne[0].Aliases, "mutated")
	copyOne[0].Qualifiers = append(copyOne[0].Qualifiers, "mutated")
	if slices.Contains(catalog.Providers()[0].Aliases, "mutated") ||
		slices.Contains(catalog.Providers()[0].Qualifiers, "mutated") {
		t.Fatal("Providers() exposed bean metadata storage")
	}
}

func TestClassifyDependencyKindsByExactGoTypeIdentity(t *testing.T) {
	t.Parallel()
	stringType := types.Typ[types.String]
	tests := []struct {
		name string
		typ  types.Type
		kind DependencyKind
		elem types.Type
	}{
		{
			name: "slice",
			typ:  types.NewSlice(stringType),
			kind: DependencySlice,
			elem: stringType,
		},
		{
			name: "string map",
			typ:  types.NewMap(stringType, stringType),
			kind: DependencyMap,
			elem: stringType,
		},
		{
			name: "non-string map",
			typ:  types.NewMap(types.Typ[types.Int], stringType),
			kind: DependencySingle,
		},
		{
			name: "optional",
			typ:  instantiatedHandleType(t, beanPackagePath, "Optional"),
			kind: DependencyOptional,
			elem: stringType,
		},
		{
			name: "lazy",
			typ:  instantiatedHandleType(t, beanPackagePath, "Lazy"),
			kind: DependencyLazy,
			elem: stringType,
		},
		{
			name: "provider",
			typ:  instantiatedHandleType(t, beanPackagePath, "Provider"),
			kind: DependencyProvider,
			elem: stringType,
		},
		{
			name: "foreign lookalike",
			typ:  instantiatedHandleType(t, "example.com/bean", "Provider"),
			kind: DependencySingle,
		},
		{
			name: "unknown bean handle",
			typ:  instantiatedHandleType(t, beanPackagePath, "Unknown"),
			kind: DependencySingle,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependency := Dependency{
				Type: test.typ,
				Kind: DependencySingle,
			}
			classifyDependency(&dependency)
			if dependency.Kind != test.kind ||
				(test.elem == nil && dependency.Element != nil) ||
				(test.elem != nil &&
					!types.Identical(dependency.Element, test.elem)) {
				t.Fatalf("dependency = %#v", dependency)
			}
			if test.elem != nil &&
				!types.Identical(dependency.MatchType(), test.elem) {
				t.Fatalf(
					"MatchType() = %v, want %v",
					dependency.MatchType(),
					test.elem,
				)
			}
		})
	}
	classifyDependency(nil)
	classifyDependency(&Dependency{})
}

func instantiatedHandleType(
	t *testing.T,
	packagePath string,
	name string,
) types.Type {
	t.Helper()
	pkg := types.NewPackage(packagePath, "bean")
	parameter := types.NewTypeParam(
		types.NewTypeName(0, pkg, "T", nil),
		types.Universe.Lookup("any").Type(),
	)
	origin := types.NewNamed(
		types.NewTypeName(0, pkg, name, nil),
		types.NewStruct(nil, nil),
		nil,
	)
	origin.SetTypeParams([]*types.TypeParam{parameter})
	value, err := types.Instantiate(
		nil,
		origin,
		[]types.Type{types.Typ[types.String]},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestInterfaceBindingDoesNotRequireHandwrittenAssertion(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/missingassertion\n\ngo 1.26.0\n",
		"app.go": `package missingassertion

type Processor interface { Process() error }

// @Service
// @Implements(Processor)
type Stripe struct{}

func (*Stripe) Process() error { return nil }
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	diagnostics := catalog.Diagnostics()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %s", strings.Join(diagnosticStrings(diagnostics), "\n"))
	}
	providers := catalog.Providers()
	if len(providers) != 1 ||
		len(providers[0].Interfaces) != 1 ||
		providers[0].Interfaces[0].Expression != "Processor" {
		t.Fatalf("providers = %#v", providers)
	}
}

func TestStereotypeConstructorDiscovery(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/constructors\n\ngo 1.26.0\n",
		"constructors.go": `package constructors

type Dependency struct{}

// @Bean
func DependencyProvider() Dependency { return Dependency{} }

// @Service(constructor=BuildExplicit)
type Explicit struct{}

func BuildExplicit(Dependency) (*Explicit, error) { return &Explicit{}, nil }

// @Service
type Conventional struct{}

func NewConventional(Dependency) *Conventional { return &Conventional{} }

// @Service
type Allocated struct{}
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	explicit := providerByName(catalog.Providers(), "explicit")
	if explicit == nil ||
		explicit.Constructor.Name != "BuildExplicit" ||
		!explicit.ReturnsError ||
		len(explicit.Dependencies) != 1 {
		t.Fatalf("explicit provider = %#v", explicit)
	}
	conventional := providerByName(catalog.Providers(), "conventional")
	if conventional == nil ||
		conventional.Constructor.Name != "NewConventional" ||
		conventional.Construction != ConstructionFactory {
		t.Fatalf("conventional provider = %#v", conventional)
	}
	allocated := providerByName(catalog.Providers(), "allocated")
	if allocated == nil ||
		allocated.Construction != ConstructionAllocate ||
		allocated.OutputTypeID !=
			"*example.com/constructors.Allocated" {
		t.Fatalf("allocated provider = %#v", allocated)
	}
}

func TestStereotypeValidationFailsClosed(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/badstereotypes\n\ngo 1.26.0\n",
		"invalid.go": `package badstereotypes

type Base struct{}

// @Service
type Alias = Base

// @Service
type private struct{}

// @Service
type Generic[T any] struct{}

// @Service
type Scalar string

// @Service
type Wrong struct{}

func NewWrong() int { return 1 }

// @Service(constructor=Missing)
type MissingConstructor struct{}

var Missing int
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
	for _, expected := range []string{
		"defined named type",
		"must be exported",
		"must not declare type parameters",
		"struct underlying type",
		"must return Wrong or *Wrong",
		"does not name a function",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestInterfaceBindingValidationFailsClosed(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/badinterfaces\n\ngo 1.26.0\n",
		"invalid.go": `package badinterfaces

type Processor interface { Process() error }
type NotInterface struct{}
type Constraint interface { ~int }

// @Service
// @Implements(Processor, Missing, NotInterface, Constraint)
type Valid struct{}

func (*Valid) Process() error { return nil }

// @Service(constructor=NewValue)
// @Implements(Processor)
type Value struct{}

func NewValue() Value { return Value{} }
func (*Value) Process() error { return nil }

// @Bean
// @Implements(Processor)
func Exact() Processor { return nil }
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
	for _, expected := range []string{
		"cannot be resolved",
		"not an interface",
		"constraint-only",
		"does not implement",
		"is redundant",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestCatalogValidProviders(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/providers\n\ngo 1.23.0\n",
		"api/api.go": `package api

type Logger interface { Log(string) }
`,
		"app/providers.go": `package app

import "example.com/providers/api"

type Config struct{}
type Store struct{}
type Service struct{}
type NamedA int
type NamedB int

// @Bean
func ConfigProvider() Config { panic("provider body must not execute") }

// @Bean
func StoreProvider(config Config) *Store { panic("provider body must not execute") }

// @Bean
func ServiceProvider(config Config, logger api.Logger) (*Service, error) {
	panic("provider body must not execute")
}

// @Bean
func StringsProvider() []string { panic("provider body must not execute") }

// @Bean
func MapProvider() map[string]int { panic("provider body must not execute") }

// @Bean
func NamedAProvider() NamedA { panic("provider body must not execute") }

// @Bean
func NamedBProvider() NamedB { panic("provider body must not execute") }
`,
	})

	program, resolved := loadAndResolve(t, root, "./...")
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	providers := catalog.Providers()
	if len(providers) != 7 {
		t.Fatalf("len(Providers()) = %d, want 7", len(providers))
	}

	config := providerByName(providers, "ConfigProvider")
	if config == nil || config.ReturnsError || len(config.Dependencies) != 0 {
		t.Fatalf("ConfigProvider = %#v", config)
	}
	if config.OutputTypeID != "example.com/providers/app.Config" {
		t.Fatalf("ConfigProvider output ID = %q", config.OutputTypeID)
	}

	store := providerByName(providers, "StoreProvider")
	if store == nil || store.OutputTypeID != "*example.com/providers/app.Store" || len(store.Dependencies) != 1 {
		t.Fatalf("StoreProvider = %#v", store)
	}
	if store.Dependencies[0].TypeID != "example.com/providers/app.Config" || store.Dependencies[0].Index != 0 || store.Dependencies[0].Name != "config" {
		t.Fatalf("StoreProvider dependency = %#v", store.Dependencies[0])
	}

	service := providerByName(providers, "ServiceProvider")
	if service == nil || !service.ReturnsError || len(service.Dependencies) != 2 {
		t.Fatalf("ServiceProvider = %#v", service)
	}
	if service.Dependencies[1].TypeID != "example.com/providers/api.Logger" || service.Dependencies[1].Name != "logger" {
		t.Fatalf("ServiceProvider imported dependency = %#v", service.Dependencies[1])
	}
	if !types.Identical(service.Output, service.Symbol.Signature.Results().At(0).Type()) ||
		!types.Identical(service.Dependencies[0].Type, service.Symbol.Signature.Params().At(0).Type()) {
		t.Fatal("catalog did not retain live types from the loaded program")
	}

	wantTypes := map[string]string{
		"StringsProvider": "[]string",
		"MapProvider":     "map[string]int",
		"NamedAProvider":  "example.com/providers/app.NamedA",
		"NamedBProvider":  "example.com/providers/app.NamedB",
	}
	for name, want := range wantTypes {
		provider := providerByName(providers, name)
		if provider == nil || provider.OutputTypeID != want {
			t.Fatalf("%s output ID = %#v, want %q", name, provider, want)
		}
		if strings.Contains(provider.OutputTypeID, filepath.ToSlash(root)) {
			t.Fatalf("%s output ID leaked fixture path: %q", name, provider.OutputTypeID)
		}
	}

	copyOne := catalog.Providers()
	originalDependencyCount := len(copyOne[0].Dependencies)
	copyOne[0].Dependencies = append(copyOne[0].Dependencies, Dependency{TypeID: "mutated"})
	if len(catalog.Providers()[0].Dependencies) != originalDependencyCount {
		t.Fatal("Providers() did not return a defensive copy")
	}
}

func TestCatalogRejectsUnsupportedSignatures(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/invalidproviders\n\ngo 1.23.0\n",
		"providers.go": `package invalidproviders

type A struct{}
type B struct{}

// @Bean
func NoResult() {}

// @Bean
func OnlyError() error { return nil }

// @Bean
func TwoValues() (A, B) { return A{}, B{} }

// @Bean
func ErrorFirst() (error, A) { return nil, A{} }

// @Bean
func MultipleErrors() (A, error, error) { return A{}, nil, nil }

// @Bean
func Variadic(values ...A) B { return B{} }

// @Bean
func Generic[T any]() T { var zero T; return zero }

// @Bean
func (A) Method() B { return B{} }
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
	for _, expected := range []string{
		"must return one provided value",
		"error cannot be the only result",
		"may return only one provided value",
		"error must be the final and only additional result",
		"at most one error result",
		"variadic provider functions are not supported",
		"generic provider functions are not supported",
		"must target a package-level function",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
	if len(catalog.Providers()) != 0 {
		t.Fatalf("invalid providers entered catalog: %#v", catalog.Providers())
	}
}

func TestCatalogAllowsDuplicateOutputWithDistinctBeanNames(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod":           "module example.com/duplicates\n\ngo 1.23.0\n",
		"shared/shared.go": "package shared\n\ntype Config struct{}\n",
		"a/a.go": `package a
import "example.com/duplicates/shared"
// @Bean
func First() shared.Config { return shared.Config{} }
`,
		"a/z.go": `package a
import "example.com/duplicates/shared"
// @Bean
func Second() shared.Config { return shared.Config{} }
`,
		"b/b.go": `package b
import "example.com/duplicates/shared"
// @Bean
func Third() shared.Config { return shared.Config{} }
`,
	})
	program, resolved := loadAndResolve(t, root, "./...")
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if providers := catalog.Providers(); len(providers) != 3 {
		t.Fatalf("providers = %#v", providers)
	}
}

func TestCatalogAllowsAliasDuplicateOutputWithDistinctBeanNames(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/aliasduplicates\n\ngo 1.23.0\n",
		"providers.go": `package aliasduplicates

type Original struct{}
type Alias = Original
type Distinct Original

// @Bean
func OriginalProvider() Original { panic("provider body must not execute") }

// @Bean
func AliasProvider() Alias { panic("provider body must not execute") }

// @Bean
func DistinctProvider() Distinct { panic("provider body must not execute") }
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	providers := catalog.Providers()
	original := providerByName(providers, "OriginalProvider")
	alias := providerByName(providers, "AliasProvider")
	distinct := providerByName(providers, "DistinctProvider")
	if original == nil || alias == nil || distinct == nil {
		t.Fatalf("providers = %#v", providers)
	}
	if !types.Identical(original.Output, alias.Output) {
		t.Fatalf("alias output %q is not identical to original output %q", alias.OutputTypeID, original.OutputTypeID)
	}
	if original.OutputTypeID == alias.OutputTypeID {
		t.Fatalf("fixture does not exercise distinct alias display IDs: both are %q", original.OutputTypeID)
	}
	if types.Identical(original.Output, distinct.Output) {
		t.Fatalf("distinct named output %q unexpectedly conflicts with %q", distinct.OutputTypeID, original.OutputTypeID)
	}

	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnosticStrings(diagnostics))
	}
}

func TestCatalogAllowsSelectableEntrypointDuplicateOutputs(t *testing.T) {
	t.Parallel()

	for _, source := range []Source{
		SourceStarter,
		SourceAutoConfiguration,
	} {
		t.Run(string(source), func(t *testing.T) {
			t.Parallel()

			catalog := Add(
				Catalog{},
				Provider{
					Source:       source,
					SymbolID:     "first",
					Name:         "first",
					ExplicitName: true,
					Output:       types.Typ[types.String],
					OutputTypeID: "string",
				},
				Provider{
					Source:       source,
					SymbolID:     "second",
					Name:         "second",
					ExplicitName: true,
					Output:       types.Typ[types.String],
					OutputTypeID: "string",
				},
			)
			if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
				t.Fatalf("Add() diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestCatalogRejectsNonSelectableGeneratedDuplicateOutputs(t *testing.T) {
	t.Parallel()

	catalog := Add(
		Catalog{},
		Provider{
			Source:       SourceConfiguration,
			SymbolID:     "first",
			Name:         "first",
			Output:       types.Typ[types.String],
			OutputTypeID: "string",
		},
		Provider{
			Source:       SourceEvent,
			SymbolID:     "second",
			Name:         "second",
			Output:       types.Typ[types.String],
			OutputTypeID: "string",
		},
	)
	diagnostics := catalog.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Kind != "duplicate-output" {
		t.Fatalf("Add() diagnostics = %#v", diagnostics)
	}
}

func TestAddExtendsCatalogDefensivelyAndRechecksExactOutputs(t *testing.T) {
	base := Catalog{providers: []Provider{{
		Source:       SourceBean,
		SymbolID:     "z",
		Output:       types.Typ[types.String],
		OutputTypeID: "string",
		Dependencies: []Dependency{{Name: "original"}},
	}}}
	added := Provider{
		Source:       SourceConfiguration,
		SymbolID:     "a",
		Output:       types.Typ[types.Int],
		OutputTypeID: "int",
	}
	combined := Add(base, added)
	providers := combined.Providers()
	if len(providers) != 2 || providers[0].SymbolID != "a" ||
		providers[0].Source != SourceConfiguration || providers[1].SymbolID != "z" {
		t.Fatalf("Add() providers = %#v", providers)
	}
	providers[1].Dependencies[0].Name = "changed"
	if got := combined.Providers()[1].Dependencies[0].Name; got != "original" {
		t.Fatalf("Add() exposed dependency storage: %q", got)
	}
	if got := base.Providers()[0].Dependencies[0].Name; got != "original" {
		t.Fatalf("Add() mutated source catalog: %q", got)
	}

	duplicate := added
	duplicate.SymbolID = "duplicate"
	duplicate.Name = combined.Providers()[0].Name
	duplicate.ExplicitName = true
	duplicate.Source = SourceBean
	duplicate.Output = types.Typ[types.String]
	duplicate.OutputTypeID = "string"
	duplicate.Symbol.DisplayLabel = "duplicate"
	if diagnostics := Add(combined, duplicate).Diagnostics(); len(diagnostics) != 1 ||
		diagnostics[0].Kind != "duplicate-bean-name" ||
		!strings.Contains(diagnostics[0].Message, "bean name or alias") {
		t.Fatalf("duplicate diagnostics = %#v", diagnostics)
	}

	existing := Catalog{diagnostics: []Diagnostic{{Kind: "existing", Message: "preserved"}}}
	if diagnostics := Add(existing, added).Diagnostics(); len(diagnostics) != 1 ||
		diagnostics[0].Kind != "existing" {
		t.Fatalf("existing diagnostics = %#v", diagnostics)
	}
}

func TestCatalogDeterministic(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/deterministic\n\ngo 1.23.0\n",
		"z.go": `package deterministic

type Z struct{}
// @Bean
func ZProvider() Z { return Z{} }
`,
		"a.go": `package deterministic

type A struct{}
// @Bean
func AProvider() A { return A{} }
`,
	})
	var first string
	for run := range 5 {
		program, resolved := loadAndResolve(t, root, ".")
		catalog := buildQuiet(t, program, resolved)
		if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
			t.Fatalf("run %d diagnostics = %v", run, diagnosticStrings(diagnostics))
		}
		summary := catalogSummary(catalog)
		if run == 0 {
			first = summary
		} else if summary != first {
			t.Fatalf("run %d summary changed:\nfirst=%s\nnext=%s", run, first, summary)
		}
	}
	if !strings.Contains(first, "AProvider") || !strings.Contains(first, "ZProvider") || strings.Index(first, "AProvider") > strings.Index(first, "ZProvider") {
		t.Fatalf("provider summary is not stable symbol order: %s", first)
	}
}

func buildQuiet(t *testing.T, program *load.Program, resolved resolve.Result) Catalog {
	t.Helper()
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	catalog := Build(program, resolved)
	os.Stdout, os.Stderr = originalStdout, originalStderr
	if closeErr := stdoutWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if closeErr := stderrWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	stdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatal(err)
	}
	if len(stdout) != 0 || len(stderr) != 0 {
		t.Fatalf("provider library wrote stdout=%q stderr=%q", stdout, stderr)
	}
	return catalog
}

func loadAndResolve(t *testing.T, root string, patterns ...string) (*load.Program, resolve.Result) {
	t.Helper()
	program, err := load.Load(context.Background(), load.Options{Dir: root}, patterns...)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolved := resolve.Annotations(program)
	if len(resolved.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolved.Diagnostics)
	}
	resolved, err = testannotation.AttachOfficial(resolved)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	return program, resolved
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(files[path]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func providerByName(providers []Provider, name string) *Provider {
	for i := range providers {
		if providers[i].Name == name {
			return &providers[i]
		}
	}
	return nil
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for i, diagnostic := range diagnostics {
		result[i] = diagnostic.Error()
	}
	return result
}

func catalogSummary(catalog Catalog) string {
	var builder strings.Builder
	for _, provider := range catalog.Providers() {
		builder.WriteString(provider.SymbolID)
		builder.WriteByte('|')
		builder.WriteString(provider.OutputTypeID)
		fmt.Fprintf(&builder, "|cleanup=%t|error=%t", provider.ReturnsCleanup, provider.ReturnsError)
		for _, dependency := range provider.Dependencies {
			builder.WriteByte('|')
			builder.WriteString(dependency.TypeID)
		}
		builder.WriteByte('\n')
	}
	for _, diagnostic := range catalog.Diagnostics() {
		builder.WriteString(diagnostic.Error())
		builder.WriteByte('\n')
	}
	return builder.String()
}

func TestCatalogAcceptsCleanupSignatures(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module github.com/spice-framework/spice\n\ngo 1.23.0\n",
		"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
		"app/providers.go": `package app

import life "github.com/spice-framework/spice/lifecycle"

type Config struct{}
type PlainValue struct{}
type ErrorValue struct{}
type CleanupValue struct{}
type CleanupErrorValue struct{}
type AliasValue struct{}
type AliasErrorValue struct{}
type CleanupAlias = life.Cleanup
type ErrorAlias = error

// @Bean
func ConfigProvider() Config { panic("provider body must not execute") }

// @Bean
func PlainProvider() PlainValue { panic("provider body must not execute") }

// @Bean
func ErrorProvider(Config) (ErrorValue, error) { panic("provider body must not execute") }

// @Bean
func CleanupProvider(Config) (CleanupValue, life.Cleanup) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func CleanupErrorProvider(Config) (CleanupErrorValue, life.Cleanup, error) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func AliasProvider(Config) (AliasValue, CleanupAlias) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func AliasErrorProvider(Config) (AliasErrorValue, CleanupAlias, ErrorAlias) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func CleanupAsOutputProvider() life.Cleanup {
	panic("provider body must not execute")
}
`,
	})

	program, resolved := loadAndResolve(t, root, "./app")
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	providers := catalog.Providers()
	checks := map[string]struct {
		cleanup bool
		err     bool
	}{
		"PlainProvider":           {},
		"ErrorProvider":           {err: true},
		"CleanupProvider":         {cleanup: true},
		"CleanupErrorProvider":    {cleanup: true, err: true},
		"AliasProvider":           {cleanup: true},
		"AliasErrorProvider":      {cleanup: true, err: true},
		"CleanupAsOutputProvider": {},
	}
	for name, want := range checks {
		item := providerByName(providers, name)
		if item == nil {
			t.Fatalf("missing provider %s in %#v", name, providers)
		}
		if item.ReturnsCleanup != want.cleanup || item.ReturnsError != want.err {
			t.Fatalf("%s flags cleanup=%v error=%v, want cleanup=%v error=%v", name, item.ReturnsCleanup, item.ReturnsError, want.cleanup, want.err)
		}
	}
	cleanupOutput := providerByName(providers, "CleanupAsOutputProvider")
	if cleanupOutput == nil {
		t.Fatal("missing CleanupAsOutputProvider")
	}
	if cleanupOutput.OutputTypeID != "github.com/spice-framework/spice/lifecycle.Cleanup" || cleanupOutput.ReturnsCleanup {
		t.Fatalf("cleanup primary output = %#v", cleanupOutput)
	}
	cleanupProvider := providerByName(providers, "CleanupProvider")
	if cleanupProvider == nil {
		t.Fatal("missing CleanupProvider")
	}
	if len(cleanupProvider.Dependencies) != 1 || cleanupProvider.Dependencies[0].TypeID != "github.com/spice-framework/spice/app.Config" {
		t.Fatalf("cleanup provider dependencies = %#v", cleanupProvider.Dependencies)
	}
}

func TestCatalogRejectsInvalidCleanupSignatures(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module github.com/spice-framework/spice\n\ngo 1.23.0\n",
		"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
		"app/providers.go": `package app

import (
	"context"
	life "github.com/spice-framework/spice/lifecycle"
)

type Value struct{}
type OtherCleanup func(context.Context) error

// @Bean
func UnnamedCleanup() (Value, func(context.Context) error) { panic("must not execute") }

// @Bean
func DistinctCleanup() (Value, OtherCleanup) { panic("must not execute") }

// @Bean
func WrongShape() (Value, func() error) { panic("must not execute") }

// @Bean
func ErrorBeforeCleanup() (Value, error, life.Cleanup) { panic("must not execute") }

// @Bean
func TwoCleanup() (Value, life.Cleanup, life.Cleanup) { panic("must not execute") }

// @Bean
func TwoErrors() (Value, error, error) { panic("must not execute") }

// @Bean
func ExtraResult() (Value, life.Cleanup, error, int) { panic("must not execute") }

// @Bean
func CleanupInFinalPosition() (Value, int, life.Cleanup) { panic("must not execute") }
`,
	})

	program, resolved := loadAndResolve(t, root, "./app")
	catalog := buildQuiet(t, program, resolved)
	if len(catalog.Providers()) != 0 {
		t.Fatalf("invalid cleanup providers entered catalog: %#v", catalog.Providers())
	}
	joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
	for _, expected := range []string{
		"UnnamedCleanup", "DistinctCleanup", "WrongShape", "ErrorBeforeCleanup",
		"TwoCleanup", "TwoErrors", "ExtraResult", "CleanupInFinalPosition",
		"lifecycle.Cleanup", "accepted forms are",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestCatalogCleanupMetadataDeterministic(t *testing.T) {
	files := map[string]string{
		"go.mod": "module github.com/spice-framework/spice\n\ngo 1.23.0\n",
		"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
		"app/z.go": `package app
import life "github.com/spice-framework/spice/lifecycle"
type Z struct{}
// @Bean
func ZProvider() (Z, life.Cleanup) { panic("must not execute") }
`,
		"app/a.go": `package app
import life "github.com/spice-framework/spice/lifecycle"
type A struct{}
// @Bean
func AProvider() (A, life.Cleanup, error) { panic("must not execute") }
`,
	}
	var first string
	for run := range 20 {
		program, resolved := loadAndResolve(t, writeModule(t, files), "./app")
		catalog := buildQuiet(t, program, resolved)
		if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
			t.Fatalf("run %d diagnostics = %v", run, diagnosticStrings(diagnostics))
		}
		summary := catalogSummary(catalog)
		if run == 0 {
			first = summary
		} else if summary != first {
			t.Fatalf("run %d summary changed:\nfirst=%s\nnext=%s", run, first, summary)
		}
	}
	if !strings.Contains(first, "cleanup=true|error=false") || !strings.Contains(first, "cleanup=true|error=true") {
		t.Fatalf("cleanup flags absent from deterministic summary: %s", first)
	}
}
