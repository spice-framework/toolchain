package generate

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderGeneratesExecutableOrderedServiceMethodPolicies(t *testing.T) {
	root := servicePolicyGenerationFixture(t)
	program, model, applicationTarget := buildApplication(t, root, "./...")
	if len(model.Policies()) != 1 {
		t.Fatalf("Policies() = %#v", model.Policies())
	}
	target, diagnostics := DefaultTarget(program, applicationTarget)
	if len(diagnostics) != 0 {
		t.Fatalf("DefaultTarget() diagnostics = %v", generationDiagnosticStrings(diagnostics))
	}
	var plan Plan
	var generated, manifest []byte
	for iteration := range 10 {
		candidate, renderDiagnostics := Render(program, model, applicationTarget, target)
		if len(renderDiagnostics) != 0 {
			t.Fatalf("Render() diagnostics = %v", generationDiagnosticStrings(renderDiagnostics))
		}
		if iteration == 0 {
			plan = candidate
			generated = generatedGoContent(t, candidate)
			manifest = candidate.ManifestContent()
			continue
		}
		if !bytes.Equal(generated, generatedGoContent(t, candidate)) ||
			!bytes.Equal(manifest, candidate.ManifestContent()) {
			t.Fatalf("policy generation changed at iteration %d", iteration)
		}
	}
	writePlan(t, root, plan)
	source := generatedGoContent(t, plan)
	for _, required := range []string{
		`spiceretry "github.com/spice-framework/spice/retry"`,
		`spiceobservability "github.com/spice-framework/spice/observability"`,
		`spicesecurity "github.com/spice-framework/spice/security"`,
		`spicecache "github.com/spice-framework/spice/cache"`,
		"RetryObservers",
		"[]spiceretry.Observer",
		"MethodObservers",
		"[]spiceobservability.MethodObserver",
		"spiceretry.Run(current",
		"spiceobservability.Observe(current",
		"decorator.authorizer.Authorize(current",
		"Cache.Get(current, cacheKey)",
		"Cache.Put(current, cacheKey, result0",
		"defaultServicePolicy",
		"DefaultService orders.Service",
	} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("generated policy source missing %q:\n%s", required, source)
		}
	}
	if bytes.Contains(source, []byte("DefaultService *orders.DefaultService")) {
		t.Fatalf("generated Components exposes raw policy target:\n%s", source)
	}
	writeTestFile(
		t,
		root,
		"internal/spicegen/policygenerated/zz_spice_policy_test.go",
		generatedServicePolicyTest,
	)
	runGoTest(t, root, "./internal/spicegen/policygenerated")
}

func TestServicePolicyMetadataChangesManifestInputHash(t *testing.T) {
	var hashes []string
	for _, name := range []string{"orders.place", "orders.submit"} {
		root := servicePolicyGenerationFixtureWithObserved(t, name)
		program, model, applicationTarget := buildApplication(t, root, "./...")
		target, diagnostics := DefaultTarget(program, applicationTarget)
		if len(diagnostics) != 0 {
			t.Fatalf("DefaultTarget() diagnostics = %v", diagnostics)
		}
		plan, diagnostics := Render(program, model, applicationTarget, target)
		if len(diagnostics) != 0 {
			t.Fatalf("Render() diagnostics = %v", diagnostics)
		}
		hashes = append(hashes, plan.Manifest().InputSHA256)
	}
	if len(hashes) != 2 || hashes[0] == hashes[1] {
		t.Fatalf("policy observation metadata hashes = %v", hashes)
	}
}

func TestRenderOrdersTransactionalServiceAfterItsManager(t *testing.T) {
	root := writeModule(t, "example.com/transactionpolicy", map[string]string{
		"app/application.go": `package app

import (
	"context"

	"github.com/spice-framework/spice/data"
)

type Service interface { Execute(context.Context) error }

// @Service(constructor=NewDefaultService)
// @Implements(Service)
type DefaultService struct{}
func NewDefaultService() *DefaultService { return &DefaultService{} }

// @data.Transactional(isolation="serializable", readOnly=true)
func (*DefaultService) Execute(context.Context) error { return nil }

// @Bean
func NewManager() *data.Manager { return nil }

type Root struct{ Service Service }
// @Bean
func NewRoot(service Service) *Root { return &Root{Service: service} }

// @Application
func TransactionPolicy(*Root) {}
`,
	})
	program, model, applicationTarget := buildApplication(t, root, "./...")
	target, diagnostics := DefaultTarget(program, applicationTarget)
	if len(diagnostics) != 0 {
		t.Fatalf("DefaultTarget() diagnostics = %v", generationDiagnosticStrings(diagnostics))
	}
	plan, diagnostics := Render(program, model, applicationTarget, target)
	if len(diagnostics) != 0 {
		t.Fatalf("Render() diagnostics = %v", generationDiagnosticStrings(diagnostics))
	}
	writePlan(t, root, plan)
	providers := string(generatedFileContent(
		t,
		plan,
		"internal/spicegen/transactionpolicy/"+providersFilename,
	))
	assertOrdered(
		t,
		providers,
		"manager, managerCleanup, err :=",
		"defaultService, defaultServiceCleanup, err :=",
		"defaultServicePolicy, err :=",
	)
	for _, required := range []string{
		"decorator.manager.Within(current, spicedata.Definition{",
		"Isolation: sql.LevelSerializable",
		"ReadOnly:  true",
		"func(transactionContext context.Context, _ spicedata.Executor) error",
	} {
		if !strings.Contains(providers, required) {
			t.Fatalf("transaction policy source missing %q:\n%s", required, providers)
		}
	}
	runGoTest(t, root, "./internal/spicegen/transactionpolicy")
}

func servicePolicyGenerationFixture(t *testing.T) string {
	t.Helper()
	return servicePolicyGenerationFixtureWithObserved(t, "orders.place")
}

func servicePolicyGenerationFixtureWithObserved(t *testing.T, observedName string) string {
	t.Helper()
	return writeModule(t, "example.com/policygenerated", map[string]string{
		"orders/orders.go": `package orders

import (
	"context"
	"sync"

	"github.com/spice-framework/spice/lifecycle"
)

type Order struct { ID string }

type Service interface {
	Health() string
	Place(context.Context, string) (Order, error)
}

var state struct {
	sync.Mutex
	calls map[string]int
	cleanups int
}

type transientFailure struct{}
func (transientFailure) Error() string { return "transient" }
func (transientFailure) Transient() bool { return true }

// @Service(constructor=NewDefaultService)
// @Implements(Service)
type DefaultService struct{}

func NewDefaultService() (*DefaultService, lifecycle.Cleanup) {
	return &DefaultService{}, func(context.Context) error {
		state.Lock()
		state.cleanups++
		state.Unlock()
		return nil
	}
}

func (*DefaultService) Health() string { return "ok" }

// @observability.Observed(name="` + observedName + `")
// @security.Authorize(authenticated=true)
// @cache.Cacheable(name="orders.by-id")
// @retry.Retryable(maxAttempts=3, initialBackoff="0s", maxBackoff="0s")
func (*DefaultService) Place(ctx context.Context, id string) (Order, error) {
	if cause := context.Cause(ctx); cause != nil { return Order{}, cause }
	state.Lock()
	defer state.Unlock()
	if state.calls == nil { state.calls = make(map[string]int) }
	state.calls[id]++
	if state.calls[id] == 1 { return Order{}, transientFailure{} }
	return Order{ID: id}, nil
}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.calls = make(map[string]int)
	state.cleanups = 0
}

func Calls(id string) int {
	state.Lock()
	defer state.Unlock()
	return state.calls[id]
}

func Cleanups() int {
	state.Lock()
	defer state.Unlock()
	return state.cleanups
}
`,
		"bootstrap/application.go": `package bootstrap

import "example.com/policygenerated/orders"

type Root struct { Service orders.Service }

// @Bean
func NewRoot(service orders.Service) *Root { return &Root{Service: service} }

// @Application
func PolicyGenerated(*Root) {}
`,
	})
}

const generatedServicePolicyTest = `package spicegen

import (
	"context"
	"strings"
	"testing"

	"example.com/policygenerated/orders"
	spiceobservability "github.com/spice-framework/spice/observability"
	spiceretry "github.com/spice-framework/spice/retry"
	spicesecurity "github.com/spice-framework/spice/security"
)

type methodObserver struct { starts int; finishes int }

func (observer *methodObserver) BeginMethod(
	ctx context.Context,
	definition spiceobservability.MethodDefinition,
) (context.Context, func(spiceobservability.MethodResult)) {
	observer.starts++
	if definition.ID != "orders.place" { panic(definition.ID) }
	return ctx, func(spiceobservability.MethodResult) { observer.finishes++ }
}

func TestGeneratedServicePolicies(t *testing.T) {
	orders.Reset()
	invalid, err := NewApplicationWithOptions(context.Background(), ApplicationOptions{
		RetryObservers: []spiceretry.Observer{nil},
	})
	if err == nil || invalid != nil || !strings.Contains(err.Error(), "retry observer 0 is nil") {
		t.Fatalf("nil retry observer = (%v, %v)", invalid, err)
	}
	if orders.Cleanups() != 1 { t.Fatalf("rollback cleanups = %d", orders.Cleanups()) }

	orders.Reset()
	attempts := 0
	observed := &methodObserver{}
	application, err := NewApplicationWithOptions(context.Background(), ApplicationOptions{
		RetryObservers: []spiceretry.Observer{func(context.Context, spiceretry.Observation) { attempts++ }},
		MethodObservers: []spiceobservability.MethodObserver{observed},
	})
	if err != nil { t.Fatal(err) }
	defer func() {
		if stopErr := application.Stop(context.Background()); stopErr != nil { t.Fatal(stopErr) }
	}()
	service := application.Components().DefaultService
	if service.Health() != "ok" { t.Fatal("direct interface forwarding failed") }
	if _, callErr := service.Place(context.Background(), "42"); callErr == nil {
		t.Fatal("unauthenticated service call succeeded")
	}
	if orders.Calls("42") != 0 { t.Fatal("authorization did not guard the target") }
	principal, err := spicesecurity.NewPrincipal("user-1", "test", nil, nil)
	if err != nil { t.Fatal(err) }
	ctx, err := spicesecurity.WithPrincipal(context.Background(), principal)
	if err != nil { t.Fatal(err) }
	first, err := service.Place(ctx, "42")
	if err != nil || first.ID != "42" { t.Fatalf("first call = (%+v, %v)", first, err) }
	second, err := service.Place(ctx, "42")
	if err != nil || second != first { t.Fatalf("cached call = (%+v, %v)", second, err) }
	if orders.Calls("42") != 2 { t.Fatalf("target calls = %d", orders.Calls("42")) }
	if attempts != 2 { t.Fatalf("retry observations = %d", attempts) }
	if observed.starts != 3 || observed.finishes != 3 {
		t.Fatalf("method observations = %d/%d", observed.starts, observed.finishes)
	}
}
`
