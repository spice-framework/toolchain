package application

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/lifecycle"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	runtimeconfig "github.com/StevenBuglione/spice/config"
)

func TestBuildAssemblesDeterministicApplicationIR(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module github.com/StevenBuglione/spice\n\ngo 1.26.0\n",
		"lifecycle/cleanup.go": `package lifecycle

import "context"

type Cleanup func(context.Context) error
`,
		"app/application.go": `package app

import (
	"context"

	"github.com/StevenBuglione/spice/lifecycle"
)

type Config struct{}
type Server struct{}
type ServerAlias = *Server

// @Bean
func ZConfig() Config {
	panic("provider bodies must not execute during analysis")
}

// @OnStart
func (Config) Start(context.Context) error {
	panic("lifecycle bodies must not execute during analysis")
}

// @OnStop
func (Config) Stop(context.Context) error {
	panic("lifecycle bodies must not execute during analysis")
}

// @Bean
func AServer(Config) (ServerAlias, lifecycle.Cleanup, error) {
	panic("provider bodies must not execute during analysis")
}

// @OnStart
func (*Server) Start(context.Context) error {
	panic("lifecycle bodies must not execute during analysis")
}

// @OnStop
func (*Server) Stop(context.Context) error {
	panic("lifecycle bodies must not execute during analysis")
}

// @Application
func Web(server ServerAlias) {
	panic("application marker bodies must not execute during analysis")
}

// @Application
func Worker(Config) {
	panic("application marker bodies must not execute during analysis")
}
`,
	})

	program, resolution := loadAndResolve(t, root, "./app")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if len(model.Modules()) != 0 ||
		len(model.ModuleEdges()) != 0 ||
		len(model.ModuleCycles()) != 0 ||
		len(model.UnassignedPackages()) != 0 {
		t.Fatal("application without @Module markers has module metadata")
	}

	providers := model.Providers()
	if got := providerNames(providers); !slices.Equal(got, []string{"ZConfig", "AServer"}) {
		t.Fatalf("Providers() = %v", got)
	}
	if providers[0].ReturnsCleanup || providers[0].ReturnsError {
		t.Fatalf("ZConfig metadata = cleanup:%t error:%t", providers[0].ReturnsCleanup, providers[0].ReturnsError)
	}
	if !providers[1].ReturnsCleanup || !providers[1].ReturnsError {
		t.Fatalf("AServer metadata = cleanup:%t error:%t", providers[1].ReturnsCleanup, providers[1].ReturnsError)
	}

	edges := model.Edges()
	if len(edges) != 1 ||
		edges[0].Consumer().Name != "AServer" ||
		edges[0].Dependency().Name != "ZConfig" {
		t.Fatalf("Edges() = %#v", edges)
	}

	components := model.Components()
	if got := componentProviderNames(components); !slices.Equal(got, []string{"ZConfig", "AServer"}) {
		t.Fatalf("Components() = %v", got)
	}
	for _, component := range components {
		if component.Start == nil || component.Stop == nil {
			t.Fatalf("component %s hooks = start:%#v stop:%#v", component.Provider.Name, component.Start, component.Stop)
		}
	}

	targets := model.Targets()
	if got := targetNames(targets); !slices.Equal(got, []string{"Web", "Worker"}) {
		t.Fatalf("Targets() = %v", got)
	}
	if roots := targets[0].Roots(); len(roots) != 1 ||
		roots[0].Name != "server" ||
		roots[0].ProviderID != providers[1].SymbolID ||
		roots[0].TypeID != "github.com/StevenBuglione/spice/app.ServerAlias" {
		t.Fatalf("Web roots = %#v", roots)
	}
	if roots := targets[1].Roots(); len(roots) != 1 ||
		roots[0].ProviderID != providers[0].SymbolID {
		t.Fatalf("Worker roots = %#v", roots)
	}
}

func TestBuildIncludesValidatedModuleArchitecture(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/modules\n\ngo 1.26.0\n",
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory

type Store struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/modules/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import "example.com/modules/inventory"

var Store inventory.Store
`,
		"shared/shared.go": "package shared\n",
	})
	program, resolution := loadAndResolve(t, root, "./...")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if got := len(model.Modules()); got != 2 {
		t.Fatalf("module count = %d, want 2", got)
	}
	if got := len(model.ModuleEdges()); got != 1 {
		t.Fatalf("module edge count = %d, want 1", got)
	}
	if got := len(model.ModuleCycles()); got != 0 {
		t.Fatalf("module cycle count = %d, want 0", got)
	}
	unassigned := model.UnassignedPackages()
	if len(unassigned) != 1 || unassigned[0].Path != "example.com/modules/shared" {
		t.Fatalf("unassigned packages = %#v", unassigned)
	}
}

func TestBuildStopsAtModuleArchitectureErrors(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/modules\n\ngo 1.26.0\n",
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory
`,
		"inventory/storage/storage.go": `package storage

type Store struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/modules/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import "example.com/modules/inventory/storage"

var Store storage.Store
`,
	})
	program, resolution := loadAndResolve(t, root, "./...")
	model := Build(program, resolution)
	diagnostics := model.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Stage != StageModule ||
		diagnostics[0].Kind != "internal-access" ||
		!strings.Contains(diagnostics[0].Message, "imports internal package") {
		t.Fatalf("Build() diagnostics = %#v", diagnostics)
	}
	if len(model.Providers()) != 0 || len(model.Targets()) != 0 {
		t.Fatal("model continued into providers or application roots after module failure")
	}
}

func TestBuildRejectsInvalidApplicationMarkers(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/application\n\ngo 1.26.0\n",
		"app/application.go": `package app

type Service struct{}
type Contract interface{ Serve() }

// @Bean
func ServiceProvider() *Service { panic("must not execute") }

// @Application(value=true)
func Arguments(*Service) {}

// @Application
func Generic[T any](*Service) {}

// @Application
func Result(*Service) error { return nil }

// @Application
func Variadic(...*Service) {}

// @Application
func Missing(Contract) {}

// @Application
func (Service) Method() {}
`,
	})

	program, resolution := loadAndResolve(t, root, "./app")
	model := Build(program, resolution)
	diagnostics := model.Diagnostics()
	if len(diagnostics) != 6 {
		t.Fatalf("Diagnostics() = %v, want 6", diagnosticStrings(diagnostics))
	}
	for _, expected := range []string{
		"does not accept annotation arguments",
		"generic application markers are not supported",
		"must return no results",
		"must be non-variadic",
		"no @Bean provider produces that type",
		"must target a package-level function",
	} {
		if !containsDiagnostic(diagnostics, expected) {
			t.Fatalf("diagnostics %v missing %q", diagnosticStrings(diagnostics), expected)
		}
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Stage != StageApplication {
			t.Fatalf("diagnostic stage = %q, want %q", diagnostic.Stage, StageApplication)
		}
		if diagnostic.Position.Filename == "" || diagnostic.Position.Line == 0 {
			t.Fatalf("diagnostic has no source position: %#v", diagnostic)
		}
	}
	if len(model.Targets()) != 0 {
		t.Fatalf("Targets() = %#v for invalid model", model.Targets())
	}
}

func TestBuildRejectsDuplicateApplicationMarker(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/duplicate\n\ngo 1.26.0\n",
		"app/application.go": `package app

type Service struct{}

// @Bean
func ServiceProvider() Service { panic("must not execute") }

// @Application
// @Application
func Duplicate(Service) { panic("must not execute") }
`,
	})

	program, resolution := loadAndResolve(t, root, "./app")
	model := Build(program, resolution)
	diagnostics := model.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Kind != "duplicate-annotation" ||
		!strings.Contains(diagnostics[0].Message, "declared more than once") {
		t.Fatalf("Diagnostics() = %#v", diagnostics)
	}
}

func TestBuildAllowsPackagesWithoutApplicationMarker(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/library\n\ngo 1.26.0\n",
		"library/library.go": `package library

type Value struct{}

// @Bean
func ValueProvider() Value { panic("must not execute") }
`,
	})
	program, resolution := loadAndResolve(t, root, "./library")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if len(model.Targets()) != 0 || len(model.Providers()) != 1 {
		t.Fatalf("targets=%d providers=%d", len(model.Targets()), len(model.Providers()))
	}
}

func TestBuildReportsUpstreamStageWithoutContinuing(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		wantStage Stage
		want      string
	}{
		{
			name: "configuration",
			source: `package app

// @Configuration(prefix="server")
type Settings struct {
	Port uint ` + "`spice:\"port\"`" + `
}
`,
			wantStage: StageConfiguration,
			want:      "unsupported type",
		},
		{
			name: "provider",
			source: `package app

// @Bean
func Broken() error { return nil }
`,
			wantStage: StageProvider,
			want:      "error cannot be the only result",
		},
		{
			name: "graph",
			source: `package app

// @Bean
func Broken(string) int { return 0 }
`,
			wantStage: StageGraph,
			want:      "requires exact type string",
		},
		{
			name: "controller",
			source: `package app

// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Get("relative")
func (*API) Get() {}
`,
			wantStage: StageController,
			want:      "route paths must be absolute",
		},
		{
			name: "lifecycle",
			source: `package app

import "context"

type Service struct{}

// @Bean
func ServiceProvider() Service { return Service{} }

// @OnStop
func (Service) Stop(context.Context) error { return nil }
`,
			wantStage: StageLifecycle,
			want:      "has no corresponding @OnStart",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeModule(t, map[string]string{
				"go.mod":             "module example.com/stages\n\ngo 1.26.0\n",
				"app/application.go": test.source,
			})
			program, resolution := loadAndResolve(t, root, "./app")
			model := Build(program, resolution)
			diagnostics := model.Diagnostics()
			if len(diagnostics) != 1 ||
				diagnostics[0].Stage != test.wantStage ||
				!strings.Contains(diagnostics[0].Message, test.want) {
				t.Fatalf("Diagnostics() = %#v", diagnostics)
			}
			if len(model.Providers()) != 0 ||
				len(model.Edges()) != 0 ||
				len(model.Components()) != 0 ||
				len(model.Configurations()) != 0 ||
				len(model.Controllers()) != 0 ||
				len(model.Targets()) != 0 {
				t.Fatal("invalid upstream stage leaked a partial application model")
			}
		})
	}
}

func TestBuildIncludesTypedControllersInApplicationIR(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/httpapp\n\ngo 1.26.0\n",
		"app/application.go": `package app

import "net/http"

// @Controller(prefix="/api")
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Get("/health")
func (*API) Health(http.ResponseWriter, *http.Request) {}
`,
	})
	program, resolution := loadAndResolve(t, root, "./app")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	controllers := model.Controllers()
	if len(controllers) != 1 || controllers[0].Name != "API" ||
		controllers[0].ProviderID == "" {
		t.Fatalf("Controllers() = %#v", controllers)
	}
	routes := controllers[0].Routes()
	if len(routes) != 1 || routes[0].Path != "/api/health" || !routes[0].Raw {
		t.Fatalf("controller routes = %#v", routes)
	}
	routes[0].Name = "changed"
	if model.Controllers()[0].Routes()[0].Name == "changed" {
		t.Fatal("Controllers returned mutable route storage")
	}
}

func TestBuildIncludesTypedConfigurationInApplicationIR(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/configapp\n\ngo 1.26.0\n",
		"app/application.go": `// Package app owns the application.
//
// @Module
package app

// @Configuration(prefix="server")
type Settings struct {
	Port int ` + "`spice:\"port,default=8080\"`" + `
}

type Server struct {
	Settings Settings
}

// @Bean
func NewServer(settings Settings) Server {
	return Server{Settings: settings}
}

// @Application
func Application(Server) {}
`,
	})
	program, resolution := loadAndResolve(t, root, "./app")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	configTypes := model.Configurations()
	if len(configTypes) != 1 || configTypes[0].Name != "Settings" ||
		configTypes[0].Module != "example.com/configapp/app" {
		t.Fatalf("Configurations() = %#v", configTypes)
	}
	fields := configTypes[0].Fields()
	if len(fields) != 1 || fields[0].Key != "server.port" ||
		fields[0].Kind != runtimeconfig.KindInteger {
		t.Fatalf("configuration fields = %#v", fields)
	}
	providers := model.Providers()
	if got := providerNames(providers); !slices.Equal(got, []string{"Settings", "NewServer"}) {
		t.Fatalf("configuration provider order = %v", got)
	}
	if providers[0].Source != provider.SourceConfiguration ||
		providers[1].Source != provider.SourceBean ||
		len(model.Edges()) != 1 ||
		model.Edges()[0].Dependency().SymbolID != configTypes[0].SymbolID {
		t.Fatalf("configuration provider graph = providers:%#v edges:%#v", providers, model.Edges())
	}
	if roots := model.Targets()[0].Roots(); len(roots) != 1 || roots[0].ProviderID != providers[1].SymbolID {
		t.Fatalf("application roots = %#v", roots)
	}
	fields[0].Key = "changed"
	if model.Configurations()[0].Fields()[0].Key == "changed" {
		t.Fatal("Configurations returned mutable field storage")
	}
}

func TestBuildRejectsResolutionDiagnosticsAndNilProgram(t *testing.T) {
	resolution := resolve.Result{Diagnostics: []resolve.Diagnostic{{
		Kind:    "syntax",
		Message: "broken annotation",
	}}}
	model := Build(&load.Program{}, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 1 ||
		diagnostics[0].Stage != StageResolution ||
		diagnostics[0].Message != "broken annotation" {
		t.Fatalf("resolution diagnostics = %#v", diagnostics)
	}

	model = Build(nil, resolve.Result{})
	if diagnostics := model.Diagnostics(); len(diagnostics) != 1 ||
		diagnostics[0].Kind != "internal" ||
		!strings.Contains(diagnostics[0].Message, "loaded program") {
		t.Fatalf("nil diagnostics = %#v", diagnostics)
	}
}

func TestModelAccessorsReturnDefensiveCopies(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/copies\n\ngo 1.26.0\n",
		"app/application.go": `package app

type Dependency struct{}
type Root struct{}

// @Bean
func DependencyProvider() Dependency { return Dependency{} }

// @Bean
func RootProvider(Dependency) Root { return Root{} }

// @Application
func Application(Root) {}
`,
	})
	program, resolution := loadAndResolve(t, root, "./app")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}

	providers := model.Providers()
	providers[1].Dependencies[0].Name = "changed"
	if model.Providers()[1].Dependencies[0].Name == "changed" {
		t.Fatal("Providers returned mutable dependency storage")
	}

	targets := model.Targets()
	roots := targets[0].Roots()
	roots[0].ProviderID = "changed"
	if model.Targets()[0].Roots()[0].ProviderID == "changed" {
		t.Fatal("Targets returned mutable root storage")
	}

	invalid := Build(nil, resolve.Result{})
	diagnostics := invalid.Diagnostics()
	diagnostics[0].Message = "changed"
	if invalid.Diagnostics()[0].Message == "changed" {
		t.Fatal("Diagnostics returned mutable storage")
	}
}

func loadAndResolve(t *testing.T, root string, patterns ...string) (*load.Program, resolve.Result) {
	t.Helper()
	program, err := load.Load(context.Background(), load.Options{Dir: root}, patterns...)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	return program, resolution
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

func providerNames(providers []provider.Provider) []string {
	result := make([]string, len(providers))
	for index, item := range providers {
		result[index] = item.Name
	}
	return result
}

func targetNames(targets []Target) []string {
	result := make([]string, len(targets))
	for index, target := range targets {
		result[index] = target.Name
	}
	return result
}

func componentProviderNames(components []lifecycle.Component) []string {
	result := make([]string, len(components))
	for index, component := range components {
		result[index] = component.Provider.Name
	}
	return result
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}

func containsDiagnostic(diagnostics []Diagnostic, text string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, text) {
			return true
		}
	}
	return false
}
