package service

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/compiler/load"
	compilerstarter "github.com/StevenBuglione/spice/compiler/starter"
	publicstarter "github.com/StevenBuglione/spice/starter"
)

func TestServiceAnalyzesOverlayWithoutFilesystemWrites(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	original, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	service, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	registerServiceCleanup(t, service)

	invalid := strings.Replace(
		string(original),
		"// @Application",
		"// @Application\n// @Unknown",
		1,
	)
	invalidResult, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainPath: {Version: 7, Content: []byte(invalid)},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze(invalid) error = %v", err)
	}
	if invalidResult.Diagnostics().Empty() {
		t.Fatal("Analyze(invalid) diagnostics are empty")
	}
	if invalidResult.GenerationReady() {
		t.Fatal("Analyze(invalid) GenerationReady() = true, want false")
	}
	diagnostic := invalidResult.Diagnostics().Items()[0]
	if !strings.Contains(diagnostic.Message, "is not imported") ||
		diagnostic.Location.Path != filepath.ToSlash(mainPath) {
		t.Fatalf("invalid diagnostic = %+v", diagnostic)
	}

	uriPath := filepath.ToSlash(mainPath)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	mainURI := (&url.URL{
		Scheme: "file",
		Path:   uriPath,
	}).String()
	result, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainURI: {Version: 8, Content: original},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze(valid) error = %v", err)
	}
	if !result.Diagnostics().Empty() {
		t.Fatalf("Analyze(valid) diagnostics = %+v", result.Diagnostics().Items())
	}
	if !result.GenerationReady() {
		t.Fatal("Analyze(valid) GenerationReady() = false, want true")
	}
	plan, found := result.GenerationPlan()
	if !found || plan.Target().PackagePath != "example.com/servicefixture" {
		t.Fatalf("GenerationPlan() = %+v, %t", plan.Target(), found)
	}
	if len(plan.Files()) == 0 {
		t.Fatal("GenerationPlan() files are empty")
	}
	if len(result.ApplicationModel().Targets()) != 1 {
		t.Fatalf(
			"ApplicationModel().Targets() = %d, want 1",
			len(result.ApplicationModel().Targets()),
		)
	}
	if len(result.Annotations()) < 3 {
		t.Fatalf("Annotations() = %d, want at least 3", len(result.Annotations()))
	}
	if len(result.ProviderGraph().Providers) != 1 {
		t.Fatalf(
			"ProviderGraph().Providers = %d, want 1",
			len(result.ProviderGraph().Providers),
		)
	}
	if len(result.ModuleGraph().Modules) != 1 {
		t.Fatalf(
			"ModuleGraph().Modules = %d, want 1",
			len(result.ModuleGraph().Modules),
		)
	}
	configurations := result.Configurations()
	if len(configurations) != 1 ||
		len(configurations[0].Fields) != 1 ||
		configurations[0].Fields[0].Key != "orders.limit" {
		t.Fatalf("Configurations() = %+v", configurations)
	}
	if len(result.AnnotationDefinitions()) == 0 {
		t.Fatal("AnnotationDefinitions() are empty")
	}
	for _, definition := range result.AnnotationDefinitions() {
		if definition.Name == "management.Enable" {
			t.Fatal(
				"AnnotationDefinitions() exposed an unimported built-in",
			)
		}
	}
	for _, relativePath := range []string{
		"zz_spice_gen.go",
		".spice/servicefixture.manifest.json",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relativePath))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("overlay analysis wrote %s: %v", relativePath, statErr)
		}
	}
}

func TestServiceOwnsTheCompleteGoInterfaceCatalog(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	writeServiceFixtureFile(t, root, "payments/payments.go", `package payments

type Processor interface {
	Process() error
}

type Repository[T any] interface {
	Save(T) error
}

type constraint interface {
	~int
}

type Concrete struct{}
`)
	service, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root,
		Mode:          AnalysisValidate,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("Analyze() diagnostics = %+v", diagnostics)
	}
	catalog := result.GoInterfaces()
	var payments *GoInterfacePackage
	paymentsIndex := -1
	for index := range catalog.Packages {
		if catalog.Packages[index].Path ==
			"example.com/servicefixture/payments" {
			payments = &catalog.Packages[index]
			paymentsIndex = index
			break
		}
	}
	if payments == nil {
		t.Fatalf("GoInterfaces() packages = %+v", catalog.Packages)
	}
	if got := []string{
		payments.Interfaces[0].Name,
		payments.Interfaces[1].Name,
	}; !slices.Equal(got, []string{"Processor", "Repository"}) {
		t.Fatalf("interface names = %v", got)
	}
	if payments.Interfaces[0].TypeID !=
		"example.com/servicefixture/payments.Processor" ||
		len(payments.Interfaces[0].Methods) != 1 ||
		payments.Interfaces[0].Methods[0].Name != "Process" ||
		!payments.Interfaces[0].HasLocation {
		t.Fatalf("Processor = %+v", payments.Interfaces[0])
	}
	if !slices.Equal(
		payments.Interfaces[1].TypeParameters,
		[]string{"T"},
	) {
		t.Fatalf(
			"Repository type parameters = %v",
			payments.Interfaces[1].TypeParameters,
		)
	}

	// Result accessors must not expose the cached compiler catalog.
	catalog.Packages[paymentsIndex].Files = nil
	catalog.Packages[paymentsIndex].Interfaces[0].Methods = nil
	again := result.GoInterfaces()
	if len(again.Packages[paymentsIndex].Files) == 0 ||
		len(again.Packages[paymentsIndex].Interfaces[0].Methods) == 0 {
		t.Fatal("GoInterfaces() exposed mutable catalog storage")
	}
}

func TestServiceValidationModeDoesNotRequireApplicationTarget(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	writeServiceFixtureFile(t, root, "main.go", `package main

// @import { Service } from "github.com/StevenBuglione/spice/annotation/core"

// @Service
type ProcessBoundary struct{}
`)
	compiler, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	registerServiceCleanup(t, compiler)
	result, err := compiler.Analyze(context.Background(), Request{
		WorkspaceRoot: root,
		Mode:          AnalysisValidate,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !result.Diagnostics().Empty() ||
		result.GenerationReady() ||
		result.TargetName() != "" ||
		result.Files() != 3 {
		t.Fatalf(
			"validation result diagnostics=%+v generation=%t target=%q files=%d",
			result.Diagnostics().Items(),
			result.GenerationReady(),
			result.TargetName(),
			result.Files(),
		)
	}
}

func TestServiceRejectsInvalidAnalysisModes(t *testing.T) {
	t.Parallel()
	compiler, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	root := writeServiceModule(t)
	for _, request := range []Request{
		{WorkspaceRoot: root, Mode: AnalysisMode(255)},
		{
			WorkspaceRoot: root,
			Mode:          AnalysisValidate,
			Target:        "Application",
		},
	} {
		if _, err := compiler.Analyze(context.Background(), request); err == nil {
			t.Fatalf("Analyze(%+v) error = nil, want failure", request)
		}
	}
}

func TestServiceLoadsAndDecodesExplicitAnnotationImports(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	writeServiceFixtureFile(t, root, "main.go", `package main

import "os"

// @import { Application as SpiceApplication } from "github.com/StevenBuglione/spice/annotation/core"

// @SpiceApplication
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`)
	compiler, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if closeErr := compiler.Close(closeCtx); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	result, err := compiler.Analyze(context.Background(), Request{
		WorkspaceRoot: root,
		Overlay: map[string]Document{
			filepath.Join(root, "main.go"): {
				Version: 1,
				Content: []byte(`package main

import "os"

// @import { Application as SpiceApplication } from "github.com/StevenBuglione/spice/annotation/core"

// @SpiceApplication
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`),
			},
		},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !result.Diagnostics().Empty() {
		t.Fatalf("diagnostics = %+v", result.Diagnostics().Items())
	}
	if !result.GenerationReady() {
		t.Fatal("GenerationReady() = false")
	}
	var imported *Annotation
	for index := range result.annotations {
		if result.annotations[index].Name == "core.Application" {
			imported = &result.annotations[index]
			break
		}
	}
	if imported == nil || imported.Raw != "// @SpiceApplication" {
		t.Fatalf("annotations = %+v", result.Annotations())
	}
	if imported.Spelling != "SpiceApplication" ||
		imported.DefinitionPackage !=
			"github.com/StevenBuglione/spice/annotation/core" ||
		imported.DefinitionSymbol != "Application" {
		t.Fatalf("imported annotation metadata = %+v", imported)
	}
	var definition *AnnotationDefinition
	for index := range result.definitions {
		if result.definitions[index].Name == "core.Application" {
			definition = &result.definitions[index]
			break
		}
	}
	if definition == nil ||
		definition.Summary == "" ||
		!strings.Contains(definition.Documentation, "Application marks") ||
		definition.DescriptorPackage != imported.DefinitionPackage ||
		definition.DescriptorSymbol != imported.DefinitionSymbol ||
		!definition.HasDescriptorLocation ||
		definition.Implementation.Tool !=
			"github.com/StevenBuglione/spice/cmd/spice-annotation-core" ||
		definition.Implementation.Handler !=
			"github.com/StevenBuglione/spice/annotation/core.ApplicationHandler" ||
		!definition.Implementation.HasLocation ||
		!strings.HasSuffix(
			definition.Implementation.Location.Path,
			"/annotation/core/application.go",
		) ||
		definition.Provenance.Module !=
			"github.com/StevenBuglione/spice" ||
		!definition.Provenance.LocalReplacement {
		t.Fatalf("descriptor definition metadata = %+v", definition)
	}
}

func TestServiceExplicitAnnotationFailsClosedWithoutToolAuthorization(
	t *testing.T,
) {
	t.Parallel()
	root := writeServiceModule(t)
	writeServiceFixtureFile(t, root, "main.go", `package main

import "os"

// @import { Application } from "github.com/StevenBuglione/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`)
	modPath := filepath.Join(root, "go.mod")
	content, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatalf("ReadFile(go.mod) error = %v", err)
	}
	content = bytes.Replace(
		content,
		[]byte(
			"tool github.com/StevenBuglione/spice/cmd/spice-annotation-core\n\n",
		),
		nil,
		1,
	)
	if writeErr := os.WriteFile(modPath, content, 0o600); writeErr != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", writeErr)
	}
	compiler, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := compiler.Analyze(context.Background(), Request{
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	items := result.Diagnostics().Items()
	if len(items) != 1 ||
		items[0].Code != "spice.annotation-tool.operation" ||
		!strings.Contains(items[0].Message, "go get -tool") ||
		result.GenerationReady() {
		t.Fatalf(
			"Analyze() diagnostics = %+v, ready = %t",
			items,
			result.GenerationReady(),
		)
	}
}

func TestServiceBuildsIRFromMultipleToolContributions(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	writeServiceFixtureFile(t, root, "main.go", `package main

import "os"

// @import { Application } from "github.com/StevenBuglione/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`)
	writeServiceFixtureFile(t, root, "orders/doc.go", `// Package orders owns order configuration.
// @import { Module } from "github.com/StevenBuglione/spice/annotation/modulith"
// @Module
package orders
`)
	writeServiceFixtureFile(t, root, "orders/config.go", `package orders

// @import { Bean, Configuration } from "github.com/StevenBuglione/spice/annotation/core"

// @Configuration(prefix="orders")
type Settings struct {
	Limit int `+"`spice:\"limit,default=100\"`"+`
}

type Store struct{}

// @Bean
func NewStore(Settings) *Store {
	panic("provider bodies must not execute during analysis")
}
`)
	compiler, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if closeErr := compiler.Close(closeCtx); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	result, err := compiler.Analyze(context.Background(), Request{
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !result.Diagnostics().Empty() || !result.GenerationReady() {
		t.Fatalf(
			"Analyze() diagnostics = %+v, ready = %t",
			result.Diagnostics().Items(),
			result.GenerationReady(),
		)
	}
	if len(result.ApplicationModel().Targets()) != 1 ||
		len(result.ProviderGraph().Providers) != 2 ||
		len(result.Configurations()) != 1 ||
		len(result.ModuleGraph().Modules) != 1 {
		t.Fatalf(
			"tool-contributed result targets=%d providers=%d configurations=%d modules=%d",
			len(result.ApplicationModel().Targets()),
			len(result.ProviderGraph().Providers),
			len(result.Configurations()),
			len(result.ModuleGraph().Modules),
		)
	}
}

func TestAnalysisLoadOptionsDisableNetworkAndSelectModuleMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	compiler, err := New(Config{
		LoadOptions: load.Options{
			Env:        []string{"PATH=test", "GOPROXY=https://proxy.example"},
			BuildFlags: []string{"-mod=mod", "-tags=local"},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	options := compiler.analysisLoadOptions(normalizedRequest{root: root})
	if !slices.Contains(options.Env, "GOPROXY=off") ||
		slices.Contains(options.Env, "GOPROXY=https://proxy.example") {
		t.Fatalf("environment = %#v", options.Env)
	}
	if !slices.Contains(options.BuildFlags, "-mod=readonly") ||
		slices.Contains(options.BuildFlags, "-mod=mod") {
		t.Fatalf("build flags = %#v", options.BuildFlags)
	}
	vendor := filepath.Join(root, "vendor")
	if err := os.MkdirAll(vendor, 0o750); err != nil {
		t.Fatalf("MkdirAll(vendor) error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(vendor, "modules.txt"),
		[]byte("# fixture\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(modules.txt) error = %v", err)
	}
	options = compiler.analysisLoadOptions(normalizedRequest{root: root})
	if !slices.Contains(options.BuildFlags, "-mod=vendor") {
		t.Fatalf("vendor build flags = %#v", options.BuildFlags)
	}
}

func TestServiceOffersVersionedRawAnnotationCommentFix(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	original, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	raw := bytes.Replace(
		original,
		[]byte("// @Application"),
		[]byte("@Application"),
		1,
	)
	service, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainPath: {Version: 11, Content: raw},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	actions := result.CodeActions()
	if len(actions) != 1 ||
		actions[0].Title != "Convert to a valid Spice annotation comment" ||
		len(actions[0].Edits) != 1 {
		t.Fatalf("CodeActions() = %+v", actions)
	}
	edit := actions[0].Edits[0]
	if edit.NewText != "// " ||
		edit.DocumentVersion == nil ||
		*edit.DocumentVersion != 11 ||
		edit.Location.Range.Start != edit.Location.Range.End {
		t.Fatalf("CodeActions()[0].Edits[0] = %+v", edit)
	}
}

func TestServiceOffersVersionedLegacyImportReplacement(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	content := []byte(`package main

import "os"

// @spice.import { Application } from "github.com/StevenBuglione/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`)
	service, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainPath: {Version: 17, Content: content},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	items := result.Diagnostics().Items()
	if len(items) != 1 ||
		items[0].Code !=
			"spice.resolution.annotation-import-legacy" ||
		!strings.Contains(items[0].Message, "replace it with @import") {
		t.Fatalf("Diagnostics() = %+v", items)
	}
	actions := result.CodeActions()
	if len(actions) != 1 ||
		actions[0].Title !=
			"Replace @spice.import with @import" ||
		len(actions[0].Edits) != 1 {
		t.Fatalf("CodeActions() = %+v", actions)
	}
	edit := actions[0].Edits[0]
	start := edit.Location.Range.Start.Offset
	end := edit.Location.Range.End.Offset
	if edit.NewText != "@import" ||
		edit.DocumentVersion == nil ||
		*edit.DocumentVersion != 17 ||
		end-start != len("@spice.import") ||
		string(content[start:end]) != "@spice.import" {
		t.Fatalf("CodeActions()[0].Edits[0] = %+v", edit)
	}
}

func TestServiceOffersVersionedInterfaceAssertionFix(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	content := []byte(`package main

// @import { Implements, Service } from "github.com/StevenBuglione/spice/annotation/core"

type Processor interface {
	Process() error
}

// @Service
// @Implements(Processor)
type Stripe struct{}

func (*Stripe) Process() error { return nil }
`)
	writeServiceFixtureFile(t, root, "main.go", string(content))
	compiler, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if closeErr := compiler.Close(closeCtx); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	result, err := compiler.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Mode:          AnalysisValidate,
			Overlay: map[string]Document{
				mainPath: {Version: 29, Content: content},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	items := result.Diagnostics().Items()
	if len(items) != 1 ||
		items[0].Code != "spice.provider.missing-interface-assertion" {
		t.Fatalf("Diagnostics() = %+v", items)
	}
	graph := result.ProviderGraph()
	annotations := result.Annotations()
	providerID := ""
	if len(graph.Providers) == 1 {
		providerID = graph.Providers[0].ID
	}
	matchingAnnotation := false
	for _, item := range annotations {
		if item.DefinitionSymbol == "Implements" &&
			item.SymbolID == providerID {
			matchingAnnotation = true
		}
	}
	if len(graph.Providers) != 1 ||
		graph.Providers[0].AssertionValue != "(*Stripe)(nil)" ||
		!matchingAnnotation {
		t.Fatalf(
			"provider authoring metadata = graph %+v, annotations %+v",
			graph,
			annotations,
		)
	}
	actions := result.CodeActions()
	if len(actions) != 1 ||
		actions[0].Title !=
			"Add compile-time assertion for Processor" ||
		len(actions[0].Edits) != 1 {
		t.Fatalf(
			"CodeActions() = %+v, Diagnostics() = %+v",
			actions,
			items[0].Fixes,
		)
	}
	visibleSigil := bytes.Index(content, []byte("@Implements"))
	if visibleSigil < 0 ||
		actions[0].AppliesTo == nil ||
		actions[0].AppliesTo.Range.Start.Offset != visibleSigil ||
		items[0].Location.Range.Start.Offset != visibleSigil {
		t.Fatalf(
			"diagnostic/action anchors = diagnostic %+v, action %+v; want visible @ offset %d",
			items[0].Location,
			actions[0].AppliesTo,
			visibleSigil,
		)
	}
	edit := actions[0].Edits[0]
	if edit.NewText != "var _ Processor = (*Stripe)(nil)\n\n" ||
		edit.DocumentVersion == nil ||
		*edit.DocumentVersion != 29 ||
		edit.Location.Range.Start != edit.Location.Range.End {
		t.Fatalf("CodeActions()[0].Edits[0] = %+v", edit)
	}
	start := edit.Location.Range.Start.Offset
	if !strings.HasPrefix(
		string(content[start:]),
		"// @Service",
	) {
		t.Fatalf(
			"assertion insertion offset %d does not precede the annotation group",
			start,
		)
	}
	fixed := append([]byte(nil), content[:start]...)
	fixed = append(fixed, edit.NewText...)
	fixed = append(fixed, content[start:]...)
	rechecked, err := compiler.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Mode:          AnalysisValidate,
			Overlay: map[string]Document{
				mainPath: {Version: 30, Content: fixed},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze(fixed) error = %v", err)
	}
	if !rechecked.Diagnostics().Empty() {
		t.Fatalf(
			"Analyze(fixed) diagnostics = %+v",
			rechecked.Diagnostics().Items(),
		)
	}
}

func TestServiceKeepsAuthoringMetadataForIncompleteInterface(
	t *testing.T,
) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	content := []byte(`package main

// @import { Implements, Service } from "github.com/StevenBuglione/spice/annotation/core"

// @Service
// @Implements(payments.Pro)
type Stripe struct{}

// @Service
// @Implements(payments.Processor)
type ManualProcessor struct{}

func (*ManualProcessor) Process() error { return nil }
`)
	writeServiceFixtureFile(t, root, "main.go", string(content))
	writeServiceFixtureFile(t, root, "payments/payments.go", `package payments

type Processor interface {
	Process() error
}
`)
	compiler, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if closeErr := compiler.Close(closeCtx); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	result, err := compiler.Analyze(t.Context(), Request{
		WorkspaceRoot: root,
		Mode:          AnalysisValidate,
		Overlay: map[string]Document{
			mainPath: {Version: 41, Content: content},
		},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.Diagnostics().Empty() {
		t.Fatal("Analyze() diagnostics are empty")
	}
	graph := result.ProviderGraph()
	if len(graph.Providers) != 2 ||
		graph.Providers[0].AssertionValue == "" ||
		graph.Providers[1].AssertionValue == "" {
		t.Fatalf("ProviderGraph() = %+v", graph)
	}
	var implementsDomain sdk.ValueDomain
	for _, definition := range result.AnnotationDefinitions() {
		if definition.DescriptorSymbol != "Implements" {
			continue
		}
		implementsDomain = definition.Arguments[0].ValueDomain
	}
	if implementsDomain != sdk.ValueDomainGoInterface {
		t.Fatalf("Implements value domain = %q", implementsDomain)
	}
	var incomplete Annotation
	for _, item := range result.Annotations() {
		if item.DefinitionSymbol == "Implements" &&
			item.Declaration == "Stripe" {
			incomplete = item
		}
	}
	if incomplete.SymbolID == "" ||
		incomplete.Location.Range.Start.Line != 6 {
		t.Fatalf("incomplete Implements annotation = %+v", incomplete)
	}
}

func TestServiceReportsEveryRawAnnotationAsSourceDiagnostic(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	original, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	raw := bytes.Replace(
		original,
		[]byte("// @Application"),
		[]byte(`@Application
@management.Enable(expose=["health"])
@observability.Logging`),
		1,
	)
	service, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainPath: {Version: 12, Content: raw},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	items := result.Diagnostics().Items()
	if len(items) != 3 {
		t.Fatalf("Diagnostics() = %+v, want three source diagnostics", items)
	}
	for index, item := range items {
		if item.Code != "spice.source.annotation-comment" ||
			item.Location.Path != filepath.ToSlash(mainPath) ||
			len(item.Fixes) != 1 ||
			len(item.Fixes[0].Edits) != 1 {
			t.Fatalf("Diagnostics()[%d] = %+v", index, item)
		}
		edit := item.Fixes[0].Edits[0]
		if edit.NewText != "// " ||
			edit.DocumentVersion == nil ||
			*edit.DocumentVersion != 12 ||
			edit.Location.Range.Start != edit.Location.Range.End {
			t.Fatalf("Diagnostics()[%d] edit = %+v", index, edit)
		}
	}
	if len(result.CodeActions()) != 3 {
		t.Fatalf("CodeActions() = %+v, want three fixes", result.CodeActions())
	}
}

func TestServiceDoesNotTreatRawStringContentAsAnnotationSource(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	original, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	withRawString := bytes.Replace(
		original,
		[]byte("// @Application"),
		[]byte("const example = `\n@Application\n`\n\n// @Application"),
		1,
	)
	service, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainPath: {Version: 13, Content: withRawString},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !result.Diagnostics().Empty() || !result.GenerationReady() {
		t.Fatalf(
			"Analyze(raw string) diagnostics = %+v, ready = %t",
			result.Diagnostics().Items(),
			result.GenerationReady(),
		)
	}
}

func TestServiceCacheIsBoundedAndResultsAreDefensive(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	var loads atomic.Int64
	service, err := New(Config{
		MaxCacheEntries: 1,
		Loader: func(
			ctx context.Context,
			options load.Options,
			patterns ...string,
		) (*load.Program, error) {
			loads.Add(1)
			return load.Load(ctx, options, patterns...)
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	registerServiceCleanup(t, service)
	request := Request{
		WorkspaceRoot: root,
		ContentHash:   "snapshot-1",
	}
	first, err := service.Analyze(context.Background(), request)
	if err != nil {
		t.Fatalf("Analyze(first) error = %v", err)
	}
	annotations := first.Annotations()
	annotations[0].Name = "mutated"
	definitions := first.AnnotationDefinitions()
	definitions[0].Arguments = nil
	configurations := first.Configurations()
	configurations[0].Fields[0].Key = "mutated"

	second, err := service.Analyze(context.Background(), request)
	if err != nil {
		t.Fatalf("Analyze(second) error = %v", err)
	}
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d after cache hit, want 1", loads.Load())
	}
	if second.Annotations()[0].Name == "mutated" ||
		second.Configurations()[0].Fields[0].Key == "mutated" {
		t.Fatal("cached result was mutated through a defensive getter")
	}

	mainPath := filepath.Join(root, "main.go")
	original, readErr := os.ReadFile(mainPath)
	if readErr != nil {
		t.Fatalf("ReadFile(main.go) error = %v", readErr)
	}
	changed := Request{
		WorkspaceRoot: root,
		ContentHash:   "snapshot-2",
		Overlay: map[string]Document{
			mainPath: {
				Version: 1,
				Content: append(original, '\n'),
			},
		},
	}
	if _, err := service.Analyze(context.Background(), changed); err != nil {
		t.Fatalf("Analyze(changed) error = %v", err)
	}
	if _, err := service.Analyze(context.Background(), request); err != nil {
		t.Fatalf("Analyze(evicted) error = %v", err)
	}
	if loads.Load() != 3 {
		t.Fatalf("loader calls after eviction = %d, want 3", loads.Load())
	}
}

func TestServiceRejectsStaleSequencedAnalysis(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	firstStarted := make(chan struct{})
	var calls atomic.Int64
	service, err := New(Config{
		Loader: func(
			ctx context.Context,
			_ load.Options,
			_ ...string,
		) (*load.Program, error) {
			if calls.Add(1) == 1 {
				close(firstStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return nil, errors.New("synthetic load failure")
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, analyzeErr := service.Analyze(
			context.Background(),
			Request{WorkspaceRoot: root, Sequence: 1},
		)
		firstDone <- analyzeErr
	}()
	<-firstStarted
	second, err := service.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root, Sequence: 2},
	)
	if err != nil {
		t.Fatalf("Analyze(second) error = %v", err)
	}
	if second.Diagnostics().Empty() {
		t.Fatal("Analyze(second) diagnostics are empty")
	}
	select {
	case firstErr := <-firstDone:
		if !errors.Is(firstErr, ErrStaleAnalysis) {
			t.Fatalf("Analyze(first) error = %v, want ErrStaleAnalysis", firstErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stale analysis")
	}
	if _, err := service.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root, Sequence: 1},
	); !errors.Is(err, ErrStaleAnalysis) {
		t.Fatalf("Analyze(old sequence) error = %v, want ErrStaleAnalysis", err)
	}
}

func TestServiceHonorsCallerCancellation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service, err := New(Config{
		Loader: func(
			ctx context.Context,
			_ load.Options,
			_ ...string,
		) (*load.Program, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Analyze(
		ctx,
		Request{WorkspaceRoot: root},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze() error = %v, want context.Canceled", err)
	}
}

func TestServiceComposesSelectedStarterMetadata(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	starterPath := filepath.Join(root, "starter", "starter.go")
	if mkdirErr := os.MkdirAll(filepath.Dir(starterPath), 0o750); mkdirErr != nil {
		t.Fatalf("MkdirAll(starter) error = %v", mkdirErr)
	}
	if writeErr := os.WriteFile(
		starterPath,
		[]byte("package starter\n\ntype Client struct{}\n\nfunc New() Client { return Client{} }\n"),
		0o600,
	); writeErr != nil {
		t.Fatalf("WriteFile(starter.go) error = %v", writeErr)
	}
	manifest := publicstarter.Must(publicstarter.Spec{
		Schema:    publicstarter.Schema,
		ID:        "example.com/servicefixture/starter",
		Version:   "1.0.0",
		Module:    "example.com/servicefixture",
		SpiceAPI:  publicstarter.APIVersion,
		MinimumGo: "1.26",
		License:   "Apache-2.0",
		Review:    "docs/dependency-review.md",
		Activation: publicstarter.Activation{
			Mode: publicstarter.ActivationExplicitConstructor,
			EntryPoints: []publicstarter.EntryPoint{{
				Package: "example.com/servicefixture/starter",
				Symbol:  "New",
			}},
		},
		Capabilities: []string{"fixture.client"},
		Dependencies: []publicstarter.Dependency{{
			Module:  "example.com/reviewed",
			Version: "v1.2.3",
			License: "MIT",
		}},
	})
	catalog, err := compilerstarter.New(manifest)
	if err != nil {
		t.Fatalf("starter.New() error = %v", err)
	}
	var inspections atomic.Int64
	service, err := New(Config{
		StarterCatalog: catalog,
		ModuleVersions: func(
			_ context.Context,
			options load.Options,
		) ([]compilerstarter.ModuleVersion, error) {
			inspections.Add(1)
			if options.Dir != root {
				t.Fatalf("module inspection directory = %q, want %q", options.Dir, root)
			}
			return []compilerstarter.ModuleVersion{{
				Path:    "example.com/reviewed",
				Version: "v1.2.3",
			}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !result.Diagnostics().Empty() || !result.GenerationReady() {
		t.Fatalf(
			"Analyze() diagnostics = %+v, ready = %t",
			result.Diagnostics().Items(),
			result.GenerationReady(),
		)
	}
	if result.TargetName() != "Servicefixture" {
		t.Fatalf("TargetName() = %q, want Servicefixture", result.TargetName())
	}
	if inspections.Load() != 1 {
		t.Fatalf("module inspections = %d, want 1", inspections.Load())
	}
	foundProvider := false
	for _, provider := range result.ProviderGraph().Providers {
		if provider.PackagePath == "example.com/servicefixture/starter" &&
			provider.Name == "New" {
			foundProvider = true
		}
	}
	if !foundProvider {
		t.Fatalf(
			"ProviderGraph() omitted selected starter constructor: %+v",
			result.ProviderGraph().Providers,
		)
	}

	misaligned, err := New(Config{
		StarterCatalog: catalog,
		ModuleVersions: func(
			context.Context,
			load.Options,
		) ([]compilerstarter.ModuleVersion, error) {
			return []compilerstarter.ModuleVersion{{
				Path:    "example.com/reviewed",
				Version: "v1.2.4",
			}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New(misaligned) error = %v", err)
	}
	registerServiceCleanup(t, misaligned)
	failed, err := misaligned.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root},
	)
	if err != nil {
		t.Fatalf("Analyze(misaligned) error = %v", err)
	}
	items := failed.Diagnostics().Items()
	if len(items) != 1 ||
		items[0].Code != "spice.starter.version" ||
		failed.GenerationReady() {
		t.Fatalf(
			"Analyze(misaligned) diagnostics = %+v, ready = %t",
			items,
			failed.GenerationReady(),
		)
	}
}

func TestServiceRejectsUnsafeAndOversizedRequests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service, err := New(Config{
		MaxOverlayFiles: 1,
		MaxOverlayBytes: 4,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []Request{
		{},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"a.go": {},
				"b.go": {},
			},
		},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"a.go": {Content: []byte("12345")},
			},
		},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"../outside.go": {},
			},
		},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"https://example.com/main.go": {},
			},
		},
	}
	for _, request := range tests {
		if _, err := service.Analyze(
			context.Background(),
			request,
		); err == nil {
			t.Fatalf("Analyze(%+v) error = nil, want failure", request)
		}
	}
	for _, config := range []Config{
		{MaxCacheEntries: -1},
		{MaxOverlayFiles: -1},
		{MaxOverlayBytes: -1},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("New(%+v) error = nil, want failure", config)
		}
	}
}

func BenchmarkServiceCachedOverlayAnalysis(b *testing.B) {
	root := writeServiceModule(b)
	service, err := New(Config{})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	mainPath := filepath.Join(root, "main.go")
	content, err := os.ReadFile(mainPath)
	if err != nil {
		b.Fatalf("ReadFile(main.go) error = %v", err)
	}
	request := Request{
		WorkspaceRoot: root,
		ContentHash:   "benchmark-snapshot",
		Overlay: map[string]Document{
			mainPath: {Version: 1, Content: content},
		},
	}
	if _, err := service.Analyze(context.Background(), request); err != nil {
		b.Fatalf("Analyze(warm) error = %v", err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := service.Analyze(context.Background(), request); err != nil {
			b.Fatalf("Analyze() error = %v", err)
		}
	}
}

func FuzzNormalizeOverlay(f *testing.F) {
	root := f.TempDir()
	f.Add("main.go", []byte("package main\n"), 1)
	f.Add("../outside.go", []byte("package outside\n"), 2)
	f.Add("file:///tmp/main.go", []byte("package main\n"), 3)
	f.Fuzz(func(
		t *testing.T,
		identity string,
		content []byte,
		version int,
	) {
		if len(content) > 1_024 {
			content = content[:1_024]
		}
		result, err := normalizeOverlay(
			root,
			map[string]Document{
				identity: {Version: version, Content: content},
			},
			1,
			1_024,
		)
		if err != nil {
			return
		}
		if len(result) != 1 {
			t.Fatalf("normalizeOverlay() length = %d, want 1", len(result))
		}
		for filePath, document := range result {
			relative, relativeErr := filepath.Rel(root, filePath)
			if relativeErr != nil ||
				(relative != "." && !filepath.IsLocal(relative)) {
				t.Fatalf(
					"normalized overlay escaped root: path=%q err=%v",
					filePath,
					relativeErr,
				)
			}
			if document.Version != version ||
				!bytes.Equal(document.Content, content) {
				t.Fatalf("normalized document = %+v", document)
			}
		}
	})
}

type testingTB interface {
	Helper()
	TempDir() string
	Fatalf(string, ...any)
}

func registerServiceCleanup(tb testing.TB, service *Service) {
	tb.Helper()
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			tb.Errorf("Close() error = %v", err)
		}
	})
}

func writeServiceModule(tb testingTB) string {
	tb.Helper()
	root := tb.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		tb.Fatalf("Abs(repository) error = %v", err)
	}
	files := map[string]string{
		"go.mod": "module example.com/servicefixture\n\ngo 1.26.0\n\n" +
			"tool github.com/StevenBuglione/spice/cmd/spice-annotation-core\n\n" +
			"require github.com/StevenBuglione/spice v0.0.0\n\n" +
			"replace github.com/StevenBuglione/spice => " +
			filepath.ToSlash(repository) + "\n",
		"main.go": `package main

import "os"

// @import { Application } from "github.com/StevenBuglione/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`,
		"orders/doc.go": `// @import { Module } from "github.com/StevenBuglione/spice/annotation/modulith"

// Package orders owns order configuration.
// @Module
package orders
`,
		"orders/config.go": `package orders

// @import { Configuration } from "github.com/StevenBuglione/spice/annotation/core"

// @Configuration(prefix="orders")
type Settings struct {
	Limit int ` + "`spice:\"limit,default=100\"`" + `
}
`,
	}
	for relativePath, content := range files {
		writeServiceFixtureFile(tb, root, relativePath, content)
	}
	return root
}

func writeServiceFixtureFile(
	tb testingTB,
	root string,
	relativePath string,
	content string,
) {
	tb.Helper()
	filePath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		tb.Fatalf("MkdirAll(%s) error = %v", relativePath, err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		tb.Fatalf("WriteFile(%s) error = %v", relativePath, err)
	}
}
