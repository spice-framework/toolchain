package generate

import (
	"context"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/compiler/application"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/internal/testannotation"
)

func TestImportNamesExcludeTypesUsedOnlyForSingleDependencyResolution(
	t *testing.T,
) {
	t.Parallel()
	contracts := types.NewPackage("example.com/contracts", "contracts")
	sender := types.NewNamed(
		types.NewTypeName(
			token.NoPos,
			contracts,
			"Sender",
			nil,
		),
		types.NewInterfaceType(nil, nil).Complete(),
		nil,
	)
	providers := []provider.Provider{{
		PackagePath: "example.com/application",
		Dependencies: []provider.Dependency{{
			Kind: provider.DependencySingle,
			Type: sender,
		}},
	}}
	names := importNames(
		providers,
		nil,
		nil,
		nil,
		nil,
		nil,
		make(map[string]string),
	)
	if _, imported := names[contracts.Path()]; imported {
		t.Fatalf(
			"single dependency type package %q was imported even though generated direct calls do not name it",
			contracts.Path(),
		)
	}
	providers[0].Dependencies[0].Kind = provider.DependencySlice
	names = importNames(
		providers,
		nil,
		nil,
		nil,
		nil,
		nil,
		make(map[string]string),
	)
	if names[contracts.Path()] != contracts.Name() {
		t.Fatalf(
			"collection dependency type import = %q, want %q",
			names[contracts.Path()],
			contracts.Name(),
		)
	}
}

func TestRenderGeneratesSelectionCollectionsHandlesAndScopes(
	t *testing.T,
) {
	root := writeModule(t, "example.com/beans", map[string]string{
		"components/components.go": `package components

import (
	"context"

	"github.com/StevenBuglione/spice/bean"
	"github.com/StevenBuglione/spice/lifecycle"
)

type Processor interface{ Name() string }
type processor string
func (value processor) Name() string { return string(value) }

// @Bean(name="stripe")
// @Primary
// @Order(20)
func Stripe() Processor { return processor("stripe") }

// @Bean(name="offline")
// @Fallback
// @Order(-10)
func Offline() Processor { return processor("offline") }

type Missing struct{}
type Work struct{ ID int }
type RequestValue struct{}
type SessionValue struct{}
var workCount int
var cleanupCount int

// @Bean(name="work")
// @Prototype
func NewWork() (*Work, lifecycle.Cleanup) {
	workCount++
	value := &Work{ID: workCount}
	return value, func(context.Context) error {
		cleanupCount++
		return nil
	}
}

// @Bean(name="requestValue")
// @RequestScope
func NewRequestValue() *RequestValue { return &RequestValue{} }

// @Bean(name="sessionValue")
// @SessionScope
func NewSessionValue() *SessionValue { return &SessionValue{} }

type Consumer struct {
	Processors []Processor
	ByName map[string]Processor
	Optional bean.Optional[*Missing]
	Lazy bean.Lazy[Processor]
	Work bean.Provider[*Work]
}

var Latest *Consumer

// @Bean
func NewConsumer(
	processors []Processor,
	byName map[string]Processor,
	optional bean.Optional[*Missing],
	lazy bean.Lazy[Processor],
	work bean.Provider[*Work],
) *Consumer {
	Latest = &Consumer{
		Processors: processors,
		ByName: byName,
		Optional: optional,
		Lazy: lazy,
		Work: work,
	}
	return Latest
}

func CleanupCount() int { return cleanupCount }
`,
		"bootstrap/application.go": `package bootstrap

import "example.com/beans/components"

// @Application
func Beans(*components.Consumer) {}
`,
	})
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: root},
		"./...",
	)
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolution diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatal(err)
	}
	resolution = attachBeanGenerationContributions(
		t,
		resolution,
	)
	model := application.Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf(
			"application diagnostics = %v",
			applicationDiagnosticStrings(diagnostics),
		)
	}
	targets := model.Targets()
	if len(targets) != 1 {
		t.Fatalf("targets = %#v", targets)
	}
	target, diagnostics := DefaultTarget(program, targets[0])
	if len(diagnostics) != 0 {
		t.Fatalf("target diagnostics = %v", diagnostics)
	}
	plan, diagnostics := Render(
		program,
		model,
		targets[0],
		target,
	)
	if len(diagnostics) != 0 {
		t.Fatalf("render diagnostics = %v", diagnostics)
	}
	source := string(generatedGoContent(t, plan))
	for _, expected := range []string{
		`[]components.Processor{`,
		`map[string]components.Processor{"offline":`,
		`"stripe":`,
		`spicebean.None[*components.Missing]()`,
		`spicebean.NewLazy(func(context.Context) (components.Processor, error)`,
		`spicebean.NewProvider(providerFactory`,
		`spicebean.NewScoped[*components.RequestValue](spicebean.ScopeRequest`,
		`spicebean.NewScoped[*components.SessionValue](spicebean.ScopeSession`,
		`components.NewConsumer(`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf(
				"generated source missing %q:\n%s",
				expected,
				source,
			)
		}
	}
	if strings.Contains(source, "reflect.") {
		t.Fatalf("generated source uses reflection:\n%s", source)
	}
	writePlan(t, root, plan)
	writeTestFile(
		t,
		root,
		filepath.ToSlash(filepath.Join(
			target.OutputDir,
			"zz_spice_bean_test.go",
		)),
		generatedBeanRuntimeTest,
	)
	runGoTest(t, root, "./...")
}

func attachBeanGenerationContributions(
	t *testing.T,
	result resolve.Result,
) resolve.Result {
	t.Helper()
	for index, occurrence := range result.Occurrences {
		var contributions []sdk.Contribution
		switch occurrence.Annotation.Name {
		case "Bean":
			contribution := sdk.ProviderContribution{}
			for _, argument := range occurrence.Annotation.Arguments {
				if argument.Name == "name" &&
					argument.Value.Kind == annotation.KindString {
					contribution.Name = argument.Value.String
				}
			}
			contributions = []sdk.Contribution{{
				Kind:     sdk.ContributionProvider,
				Provider: &contribution,
			}}
		case "Primary":
			contributions = beanMetadataContribution(
				sdk.BeanMetadataContribution{Primary: true},
			)
		case "Fallback":
			contributions = beanMetadataContribution(
				sdk.BeanMetadataContribution{Fallback: true},
			)
		case "Order":
			value := occurrence.Annotation.Arguments[0].
				Value.Integer
			contributions = beanMetadataContribution(
				sdk.BeanMetadataContribution{Order: &value},
			)
		case "Prototype":
			contributions = beanMetadataContribution(
				sdk.BeanMetadataContribution{
					Scope: sdk.BeanScopePrototype,
				},
			)
		case "RequestScope":
			contributions = beanMetadataContribution(
				sdk.BeanMetadataContribution{
					Scope: sdk.BeanScopeRequest,
				},
			)
		case "SessionScope":
			contributions = beanMetadataContribution(
				sdk.BeanMetadataContribution{
					Scope: sdk.BeanScopeSession,
				},
			)
		default:
			continue
		}
		updated, err := result.WithContributions(
			index,
			contributions,
		)
		if err != nil {
			t.Fatal(err)
		}
		result = updated
	}
	return result
}

func beanMetadataContribution(
	value sdk.BeanMetadataContribution,
) []sdk.Contribution {
	return []sdk.Contribution{{
		Kind:         sdk.ContributionBeanMetadata,
		BeanMetadata: &value,
	}}
}

const generatedBeanRuntimeTest = `package spicegen

import (
	"context"
	"testing"

	"example.com/beans/components"
)

func TestGeneratedBeanRuntime(t *testing.T) {
	application, err := NewApplication(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(context.Background()); stopErr != nil {
			t.Fatal(stopErr)
		}
	})
	consumer := components.Latest
	if consumer == nil || len(consumer.Processors) != 2 ||
		consumer.Processors[0].Name() != "offline" ||
		consumer.Processors[1].Name() != "stripe" {
		t.Fatalf("processors = %#v", consumer)
	}
	if len(consumer.ByName) != 2 ||
		consumer.ByName["stripe"].Name() != "stripe" ||
		consumer.ByName["offline"].Name() != "offline" {
		t.Fatalf("map = %#v", consumer.ByName)
	}
	if _, present := consumer.Optional.Get(); present {
		t.Fatal("optional dependency is present")
	}
	lazy, err := consumer.Lazy.Get(context.Background())
	if err != nil || lazy.Name() != "stripe" {
		t.Fatalf("lazy = %#v, %v", lazy, err)
	}
	first, firstCleanup, err := consumer.Work.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, secondCleanup, err := consumer.Work.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first.ID == second.ID {
		t.Fatalf("prototype values = %#v, %#v", first, second)
	}
	if err := firstCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := firstCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := secondCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if components.CleanupCount() != 2 {
		t.Fatalf("cleanup count = %d", components.CleanupCount())
	}
}
`
