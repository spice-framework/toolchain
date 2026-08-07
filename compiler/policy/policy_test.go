package policy

import (
	"context"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/modulith"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestBuildCompilesOneOrderedInterfaceBoundServiceModel(t *testing.T) {
	t.Parallel()
	program, resolution, providers := loadPolicyFixture(t, `package orders

import "context"

type Order struct{ ID string }
type Service interface {
	Health() string
	Place(context.Context, string) (Order, error)
}

// @Service(constructor=NewDefaultService)
// @Implements(Service)
type DefaultService struct{}

func NewDefaultService() *DefaultService { panic("must not execute") }
func (*DefaultService) Health() string { return "ok" }
func IsTransient(error) bool { return true }

// @observability.Observed
// @cache.Cacheable(name="orders.by-id")
// @retry.Retryable(classifier=IsTransient)
func (*DefaultService) Place(context.Context, string) (Order, error) {
	panic("must not execute")
}

type Consumer struct{}
// @Bean
func NewConsumer(Service) *Consumer { panic("must not execute") }
`)
	catalog := Build(program, resolution, providers, modulith.Model{})
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	services := catalog.Services()
	if len(services) != 1 || services[0].Provider.Role != "service" ||
		services[0].Interface.TypeID != "example.com/policy/orders.Service" ||
		services[0].Module != "example.com/policy/orders" {
		t.Fatalf("Services() = %#v", services)
	}
	methods := services[0].Methods()
	if len(methods) != 2 || methods[0].Name != "Health" || methods[0].Decorated() ||
		methods[1].Name != "Place" || methods[1].Cache == nil ||
		methods[1].Retry == nil || methods[1].Retry.Classifier != "IsTransient" ||
		methods[1].Observation == nil {
		t.Fatalf("Methods() = %#v", methods)
	}
	methods[1].Retry.MaxAttempts = 99
	if catalog.Services()[0].Methods()[1].Retry.MaxAttempts == 99 {
		t.Fatal("Services() returned mutable policy payload storage")
	}
}

func TestBuildRejectsMissingInterfaceAndRawConcreteInjectionDeterministically(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		source   string
		required string
	}{
		{
			name: "missing interface",
			source: `package orders
import "context"
// @Service(constructor=NewService)
type Service struct{}
func NewService() *Service { return &Service{} }
// @retry.Retryable
func (*Service) Run(context.Context) error { return nil }
`,
			required: "requires exactly one explicit @Implements interface",
		},
		{
			name: "raw concrete injection",
			source: `package orders
import "context"
type API interface { Run(context.Context) error }
// @Service(constructor=NewService)
// @Implements(API)
type Service struct{}
func NewService() *Service { return &Service{} }
// @retry.Retryable
func (*Service) Run(context.Context) error { return nil }
type Consumer struct{}
// @Bean
func NewConsumer(*Service) *Consumer { return &Consumer{} }
`,
			required: "would bypass generated method policies",
		},
		{
			name: "invalid retry classifier",
			source: `package orders
import "context"
type API interface { Run(context.Context) error }
// @Service(constructor=NewService)
// @Implements(API)
type Service struct{}
func NewService() *Service { return &Service{} }
// @retry.Retryable(classifier=missing)
func (*Service) Run(context.Context) error { return nil }
`,
			required: "must name an exported same-package func(error) bool",
		},
		{
			name: "non comparable cache key",
			source: `package orders
import "context"
type API interface { Find(context.Context, []string) (string, error) }
// @Service(constructor=NewService)
// @Implements(API)
type Service struct{}
func NewService() *Service { return &Service{} }
// @cache.Cacheable(name="orders.find")
func (*Service) Find(context.Context, []string) (string, error) { return "", nil }
`,
			required: "is not comparable",
		},
		{
			name: "non service retry",
			source: `package orders
import "context"
// @Component(constructor=NewWorker)
type Worker struct{}
func NewWorker() *Worker { return &Worker{} }
// @retry.Retryable
func (*Worker) Run(context.Context) error { return nil }
`,
			required: "legal only on managed @Service methods",
		},
		{
			name: "scoped service",
			source: `package orders
import "context"
type API interface { Run(context.Context) error }
// @Service(constructor=NewService)
// @Implements(API)
// @Prototype
type Service struct{}
func NewService() *Service { return &Service{} }
// @retry.Retryable
func (*Service) Run(context.Context) error { return nil }
`,
			required: "requires singleton scope",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			program, resolution, providers := loadPolicyFixture(t, test.source)
			var baseline []string
			for run := range 4 {
				if run%2 == 1 {
					slices.Reverse(resolution.Occurrences)
				}
				diagnostics := diagnosticStrings(Build(program, resolution, providers, modulith.Model{}).Diagnostics())
				if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], test.required) {
					t.Fatalf("run %d diagnostics = %v, want %q", run, diagnostics, test.required)
				}
				if run == 0 {
					baseline = diagnostics
				} else if !slices.Equal(baseline, diagnostics) {
					t.Fatalf("diagnostics changed: first=%v now=%v", baseline, diagnostics)
				}
			}
		})
	}
}

func TestPolicyValueAndTypeHelpersCoverGeneratedBoundaries(t *testing.T) {
	t.Parallel()

	if got := (Diagnostic{Message: "broken"}).Error(); got != "<unknown>:1:1: broken" {
		t.Fatalf("Diagnostic.Error() = %q", got)
	}

	method := Method{}
	transaction := sdk.TransactionContribution{Isolation: "serializable", ReadOnly: true}
	authorization := sdk.AuthorizationContribution{
		Authenticated: true,
		AnyRoles:      []string{"operator"},
		AllRoles:      []string{"auditor"},
		AllScopes:     []string{"orders:write"},
	}
	cache := sdk.CacheContribution{Name: "orders"}
	retry := sdk.RetryContribution{MaxAttempts: 3}
	observation := sdk.ObservationContribution{Name: "orders.place"}
	for _, contribution := range []sdk.Contribution{
		{Kind: sdk.ContributionTransaction, Transaction: &transaction},
		{Kind: sdk.ContributionAuthorization, Authorization: &authorization},
		{Kind: sdk.ContributionCache, Cache: &cache},
		{Kind: sdk.ContributionRetry, Retry: &retry},
		{Kind: sdk.ContributionObservation, Observation: &observation},
		{Kind: sdk.ContributionApplication},
	} {
		setContribution(&method, contribution)
	}
	authorization.AnyRoles[0] = "mutated"
	if method.Transaction == nil || method.Authorization == nil ||
		method.Authorization.AnyRoles[0] != "operator" || method.Cache == nil ||
		method.Retry == nil || method.Observation == nil {
		t.Fatalf("setContribution() = %#v", method)
	}

	service := Service{
		Provider: provider.Provider{
			Dependencies: []provider.Dependency{{Qualifiers: []string{"primary"}}},
			Interfaces:   []provider.InterfaceBinding{{Expression: "API"}},
			Aliases:      []string{"orders"},
			Qualifiers:   []string{"write"},
		},
		methods: []Method{method},
	}
	clone := service.Clone()
	clone.Provider.Dependencies[0].Qualifiers[0] = "changed"
	clone.Provider.Interfaces[0].Expression = "Changed"
	clone.Provider.Aliases[0] = "changed"
	clone.Provider.Qualifiers[0] = "changed"
	clone.methods[0].Authorization.AnyRoles[0] = "changed"
	clone.methods[0].Transaction.Isolation = "changed"
	clone.methods[0].Cache.Name = "changed"
	clone.methods[0].Retry.MaxAttempts = 99
	clone.methods[0].Observation.Name = "changed"
	if service.Provider.Dependencies[0].Qualifiers[0] != "primary" ||
		service.Provider.Interfaces[0].Expression != "API" ||
		service.Provider.Aliases[0] != "orders" || service.Provider.Qualifiers[0] != "write" ||
		service.methods[0].Authorization.AnyRoles[0] != "operator" ||
		service.methods[0].Transaction.Isolation != "serializable" ||
		service.methods[0].Cache.Name != "orders" || service.methods[0].Retry.MaxAttempts != 3 ||
		service.methods[0].Observation.Name != "orders.place" {
		t.Fatalf("Clone() changed source: %#v", service)
	}

	pkg := types.NewPackage("example.com/types", "types")
	exported := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Exported", nil), types.NewStruct(nil, nil), nil)
	unexported := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "hidden", nil), types.NewStruct(nil, nil), nil)
	emptyInterface := types.NewInterfaceType(nil, nil).Complete()
	methodSignature := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(), false)
	nonEmptyInterface := types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, pkg, "Run", methodSignature)}, nil).Complete()
	nameable := []types.Type{
		exported,
		types.Typ[types.String],
		types.NewPointer(exported),
		types.NewSlice(exported),
		types.NewArray(exported, 2),
		types.NewMap(types.Typ[types.String], exported),
		types.NewChan(types.SendRecv, exported),
		emptyInterface,
	}
	for _, value := range nameable {
		if !generatedNameable(value) {
			t.Fatalf("generatedNameable(%s) = false", value)
		}
	}
	for _, value := range []types.Type{unexported, nonEmptyInterface, methodSignature} {
		if generatedNameable(value) {
			t.Fatalf("generatedNameable(%s) = true", value)
		}
	}
	goodSignature := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, pkg, "value", types.NewPointer(exported))),
		types.NewTuple(types.NewVar(token.NoPos, pkg, "result", types.NewSlice(exported))), false)
	badSignature := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, pkg, "callback", methodSignature)),
		types.NewTuple(), false)
	if stripReceiver(nil) != nil || signatureNameable(nil) || !signatureNameable(goodSignature) || signatureNameable(badSignature) {
		t.Fatal("signature helper classification was incorrect")
	}
	managerPackage := types.NewPackage(dataPackagePath, "data")
	manager := types.NewNamed(types.NewTypeName(token.NoPos, managerPackage, "Manager", nil), types.NewStruct(nil, nil), nil)
	if pointerNamedType(types.Typ[types.String], dataPackagePath, "Manager") ||
		pointerNamedType(types.NewPointer(types.Typ[types.String]), dataPackagePath, "Manager") ||
		!pointerNamedType(types.NewPointer(manager), dataPackagePath, "Manager") {
		t.Fatal("pointerNamedType() classified a boundary type incorrectly")
	}
	if containsTransaction(nil) || !containsTransaction([]Method{{Transaction: &transaction}}) {
		t.Fatal("containsTransaction() classified policy metadata incorrectly")
	}

	for _, diagnostics := range [][]Diagnostic{
		{{PhysicalPosition: token.Position{Filename: "b"}}, {PhysicalPosition: token.Position{Filename: "a"}}},
		{{PhysicalPosition: token.Position{Filename: "a", Offset: 2}}, {PhysicalPosition: token.Position{Filename: "a", Offset: 1}}},
		{{Kind: "z"}, {Kind: "a"}},
		{{Kind: "a", ProviderID: "z"}, {Kind: "a", ProviderID: "a"}},
		{{Kind: "a", ProviderID: "a", Message: "z"}, {Kind: "a", ProviderID: "a", Message: "a"}},
	} {
		sortDiagnostics(diagnostics)
	}
}

func loadPolicyFixture(t *testing.T, source string) (*load.Program, resolve.Result, provider.Catalog) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":           "module example.com/policy\n\ngo 1.26.0\n",
		"orders/orders.go": source,
	}
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(files[name]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./...")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	providers := provider.Build(program, resolution)
	if diagnostics := providers.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("provider diagnostics = %v", diagnostics)
	}
	return program, resolution, providers
}

func diagnosticStrings(values []Diagnostic) []string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].Error()
	}
	return result
}
