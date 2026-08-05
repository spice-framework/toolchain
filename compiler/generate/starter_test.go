package generate

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation"
	publicstarter "github.com/spice-framework/spice/annotation/sdk/starter"
	"github.com/spice-framework/toolchain/compiler/application"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	compilerstarter "github.com/spice-framework/toolchain/compiler/starter"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestRenderCallsSelectedStarterEntrypointAndHashesProvenance(t *testing.T) {
	root := starterGenerationFixture(t)
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./...")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}

	firstModel := buildStarterModel(t, program, resolution, "1.2.3")
	targets := firstModel.Targets()
	if len(targets) != 1 {
		t.Fatalf("Targets() = %#v", targets)
	}
	target, diagnostics := DefaultTarget(program, targets[0])
	if len(diagnostics) != 0 {
		t.Fatalf("DefaultTarget() diagnostics = %v", diagnostics)
	}
	firstPlan, diagnostics := Render(program, firstModel, targets[0], target)
	if len(diagnostics) != 0 {
		t.Fatalf("Render() diagnostics = %v", diagnostics)
	}
	source := generatedGoContent(t, firstPlan)
	for _, expected := range []string{
		"spiceApp.Construct",
		"spiceSearch.Construct",
		`search "example.com/starterfixture/search"`,
	} {
		if !bytes.Contains(source, []byte(expected)) {
			t.Fatalf("generated source missing %q:\n%s", expected, source)
		}
	}
	assertOrdered(t, string(source), "optionsBean,", "client,", "server,")
	appSource := generatedSourceUnitContent(
		t,
		firstPlan,
		"app/application.go",
	)
	searchSource := generatedSourceUnitContent(
		t,
		firstPlan,
		"search/search.go",
	)
	for directCall, sourceUnit := range map[string][]byte{
		"app.Options()":              appSource,
		"app.NewServer(dependency0)": appSource,
		"search.New(dependency0)":    searchSource,
	} {
		if !bytes.Contains(sourceUnit, []byte(directCall)) {
			t.Fatalf(
				"generated source unit omitted %q:\n%s",
				directCall,
				sourceUnit,
			)
		}
	}

	secondModel := buildStarterModel(t, program, resolution, "1.2.4")
	secondTargets := secondModel.Targets()
	secondPlan, secondDiagnostics := Render(program, secondModel, secondTargets[0], target)
	if len(secondDiagnostics) != 0 {
		t.Fatalf("second Render() diagnostics = %v", secondDiagnostics)
	}
	if firstPlan.Manifest().InputSHA256 == secondPlan.Manifest().InputSHA256 {
		t.Fatal("starter source version did not change the generated ownership hash")
	}
	if !bytes.Equal(source, generatedGoContent(t, secondPlan)) {
		t.Fatal("starter provenance changed ordinary generated Go source")
	}

	writePlan(t, root, firstPlan)
	writeTestFile(t, root, "internal/spicegen/commerce/zz_spice_gen_test.go", starterGeneratedRuntimeTest)
	runGoTest(t, root, "./internal/spicegen/commerce")
}

func TestApplicationReportsMissingStarterDependencyWithStarterRole(t *testing.T) {
	root := starterGenerationFixture(t)
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./...")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	filtered := resolve.Result{}
	for _, occurrence := range resolution.Occurrences {
		if occurrence.Annotation.Name != "Bean" || occurrence.Name != "Options" {
			filtered.Occurrences = append(filtered.Occurrences, occurrence)
		}
	}
	catalog := provider.BuildEntrypoints(program, []provider.Entrypoint{starterEntrypoint("1.2.3")})
	model := application.BuildWithOptions(program, filtered, application.BuildOptions{
		ProviderCatalogs: []provider.Catalog{catalog},
	})
	diagnostics := model.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Stage != application.StageGraph ||
		!strings.Contains(diagnostics[0].Message, "starter entrypoint example.com/starterfixture/search.New") ||
		!strings.Contains(diagnostics[0].Message, "requires exact type example.com/starterfixture/search.Options") {
		t.Fatalf("BuildWithOptions() diagnostics = %#v", diagnostics)
	}
}

func TestRenderComposesAnnotationActivatedHTTPObserver(t *testing.T) {
	root := observerStarterFixture(t)
	program, model := observerStarterModel(t, root)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("BuildWithOptions() diagnostics = %v", diagnostics)
	}
	targets := model.Targets()
	if len(targets) != 1 {
		t.Fatalf("Targets() = %#v", targets)
	}
	target, diagnostics := DefaultTarget(program, targets[0])
	if len(diagnostics) != 0 {
		t.Fatalf("DefaultTarget() diagnostics = %v", diagnostics)
	}
	first, diagnostics := Render(program, model, targets[0], target)
	if len(diagnostics) != 0 {
		t.Fatalf("Render() diagnostics = %v", diagnostics)
	}
	second, diagnostics := Render(program, model, targets[0], target)
	if len(diagnostics) != 0 {
		t.Fatalf("Render(second) diagnostics = %v", diagnostics)
	}
	if !bytes.Equal(
		generatedGoContent(t, first),
		generatedGoContent(t, second),
	) || !bytes.Equal(
		first.ManifestContent(),
		second.ManifestContent(),
	) {
		t.Fatal("identical HTTP observer metadata changed generated output")
	}
	source := string(generatedGoContent(t, first))
	for _, expected := range []string{
		"spiceTelemetry.ConstructObserver",
		"httpObservers = append(httpObservers, dependencies.observer)",
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf(
				"generated observer source missing %q:\n%s",
				expected,
				source,
			)
		}
	}
	assertOrdered(
		t,
		string(generatedFileContent(
			t,
			first,
			"internal/spicegen/application/"+providersFilename,
		)),
		"observer,",
	)
	assertOrdered(
		t,
		string(generatedFileContent(
			t,
			first,
			"internal/spicegen/application/"+httpFilename,
		)),
		"httpObservers = append(httpObservers, dependencies.observer)",
		"registerGeneratedRoute",
	)
	if !bytes.Contains(
		generatedRoleContent(t, first, FileRoleTargetHTTP),
		[]byte("spiceweb.ObservationMiddleware"),
	) {
		t.Fatal("generated route wiring omitted HTTP observation")
	}
	telemetrySource := generatedSourceUnitContent(
		t,
		first,
		"telemetry/telemetry.go",
	)
	if !bytes.Contains(
		telemetrySource,
		[]byte("telemetry.New(dependency0)"),
	) {
		t.Fatalf(
			"generated telemetry source unit omitted direct constructor:\n%s",
			telemetrySource,
		)
	}

	writePlan(t, root, first)
	writeTestFile(
		t,
		root,
		"internal/spicegen/application/zz_spice_observer_test.go",
		generatedObserverStarterTest,
	)
	runGoTest(t, root, "./internal/spicegen/application")
}

func TestApplicationRejectsInvalidHTTPObserverStarterOutput(t *testing.T) {
	root := observerStarterFixtureWithObserver(t, false)
	_, model := observerStarterModel(t, root)
	diagnostics := model.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Stage != application.StageBootstrap ||
		diagnostics[0].Kind != "invalid-entrypoint-type" ||
		diagnostics[0].Position.Filename == "" ||
		diagnostics[0].Position.Line <= 0 ||
		!strings.Contains(
			diagnostics[0].Message,
			"does not implement exact web.HTTPObserver",
		) {
		t.Fatalf("BuildWithOptions() diagnostics = %#v", diagnostics)
	}
}

func observerStarterModel(
	t *testing.T,
	root string,
) (*load.Program, application.Model) {
	t.Helper()
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: root},
		"./...",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf(
			"resolve.Annotations() diagnostics = %v",
			resolution.Diagnostics,
		)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	manifest := observerStarterManifest(t)
	catalog, err := compilerstarter.NewWithCompatibility(
		publicstarter.APIVersion,
		"go1.26.5",
		manifest,
	)
	if err != nil {
		t.Fatalf("starter.NewWithCompatibility() error = %v", err)
	}
	starterProviders := provider.BuildEntrypoints(
		program,
		catalog.ProviderEntrypoints(resolution.Occurrences),
	)
	if diagnostics := starterProviders.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("BuildEntrypoints() diagnostics = %v", diagnostics)
	}
	model := application.BuildWithOptions(
		program,
		resolution,
		application.BuildOptions{
			ProviderCatalogs:     []provider.Catalog{starterProviders},
			BootstrapDefinitions: catalog.BootstrapDefinitions(),
		},
	)
	return program, model
}

func observerStarterManifest(t *testing.T) publicstarter.Manifest {
	t.Helper()
	return publicstarter.Must(publicstarter.Spec{
		Schema:       publicstarter.Schema,
		ID:           "example.com/observerfixture/telemetry",
		Version:      "1.0.0",
		Module:       "example.com/observerfixture",
		SpiceAPI:     publicstarter.APIVersion,
		MinimumGo:    "1.26",
		License:      "Apache-2.0",
		Review:       "docs/telemetry-review.md",
		Capabilities: []string{"observability.http-server"},
		Activation: publicstarter.Activation{
			Mode: publicstarter.ActivationExplicitAnnotation,
			EntryPoints: []publicstarter.EntryPoint{
				{
					Package: "example.com/observerfixture/telemetry",
					Symbol:  "New",
				},
			},
		},
		Annotations: []publicstarter.AnnotationSpec{
			{
				Name:    "telemetry.Enable",
				Targets: []annotation.Target{annotation.TargetFunction},
			},
		},
		ApplicationFeatures: []publicstarter.FeatureSpec{
			{
				Annotation: "telemetry.Enable",
				Capability: "observability.http-server",
				EntryPoints: []publicstarter.EntryPoint{
					{
						Package: "example.com/observerfixture/telemetry",
						Symbol:  "New",
					},
				},
				Requirements: []string{"http.serve-mux"},
			},
		},
	})
}

func observerStarterFixture(t *testing.T) string {
	t.Helper()
	return observerStarterFixtureWithObserver(t, true)
}

func observerStarterFixtureWithObserver(
	t *testing.T,
	validObserver bool,
) string {
	t.Helper()
	observerMethod := ""
	if validObserver {
		observerMethod = `
func (*Observer) BeginHTTP(
	ctx context.Context,
	route web.RouteMetadata,
) (context.Context, func(web.HTTPResult)) {
	record("begin:" + route.Pattern)
	return ctx, func(result web.HTTPResult) {
		record("end:" + strconv.Itoa(result.Status))
	}
}
`
	}
	return writeModule(t, "example.com/observerfixture", map[string]string{
		"telemetry/telemetry.go": `package telemetry

import (
	"context"
	"strconv"
	"sync"

	"github.com/spice-framework/spice/web"
)

type Options struct{}
type Observer struct{}

var (
	_ = context.Background
	_ = strconv.Itoa
	_ web.RouteMetadata
)

var state = struct {
	sync.Mutex
	events []string
}{}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.events = nil
}

func Events() []string {
	state.Lock()
	defer state.Unlock()
	return append([]string(nil), state.events...)
}

func record(event string) {
	state.Lock()
	defer state.Unlock()
	state.events = append(state.events, event)
}

func New(Options) *Observer {
	record("construct")
	return &Observer{}
}
` + observerMethod,
		"app/application.go": `package app

import (
	"context"
	"net/http"

	"example.com/observerfixture/telemetry"
)

type Request struct{}
type Response struct {
	Message string ` + "`json:\"message\"`" + `
}

// @Controller(prefix="/api")
type API struct{}

// @Bean
func NewAPI() *API {
	return &API{}
}

// @Get("/ping")
func (*API) Ping(context.Context, Request) (Response, error) {
	return Response{Message: "pong"}, nil
}

// @Bean
func TelemetryOptions() telemetry.Options {
	return telemetry.Options{}
}

// @Bean
func Mux() *http.ServeMux {
	return http.NewServeMux()
}

type Server struct{}

// @Bean
func NewServer(*http.ServeMux) *Server {
	return &Server{}
}

// @Application
// @telemetry.Enable
func Application(*Server) {
	panic("application marker must not execute during analysis")
}
`,
	})
}

const generatedObserverStarterTest = `package spicegen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"example.com/observerfixture/telemetry"
)

func TestGeneratedHTTPObserverStarter(t *testing.T) {
	telemetry.Reset()
	application, err := NewApplication(context.Background())
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	if got, want := telemetry.Events(), []string{"construct"}; !slices.Equal(got, want) {
		t.Fatalf("construction events = %v, want %v", got, want)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got, want := telemetry.Events(), []string{
		"construct",
		"begin:/api/ping",
		"end:200",
	}; !slices.Equal(got, want) {
		t.Fatalf("request events = %v, want %v", got, want)
	}
}
`

func buildStarterModel(
	t *testing.T,
	program *load.Program,
	resolution resolve.Result,
	version string,
) application.Model {
	t.Helper()
	catalog := provider.BuildEntrypoints(program, []provider.Entrypoint{starterEntrypoint(version)})
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("BuildEntrypoints() diagnostics = %v", diagnostics)
	}
	model := application.BuildWithOptions(program, resolution, application.BuildOptions{
		ProviderCatalogs: []provider.Catalog{catalog},
	})
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("BuildWithOptions() diagnostics = %v", diagnostics)
	}
	providers := model.Providers()
	if len(providers) != 3 ||
		providers[1].Source != provider.SourceStarter ||
		providers[1].SourceID != "example.com/acme/starter/search" ||
		providers[1].SourceVersion != version {
		t.Fatalf("Providers() = %#v", providers)
	}
	return model
}

func starterEntrypoint(version string) provider.Entrypoint {
	return provider.Entrypoint{
		PackagePath:   "example.com/starterfixture/search",
		Symbol:        "New",
		SourceID:      "example.com/acme/starter/search",
		SourceVersion: version,
	}
}

func starterGenerationFixture(t *testing.T) string {
	t.Helper()
	return writeModule(t, "example.com/starterfixture", map[string]string{
		"search/search.go": `package search

import (
	"context"
	"sync"

	"github.com/spice-framework/spice/lifecycle"
)

type Options struct{}
type Client struct{}

var state = struct {
	sync.Mutex
	events []string
}{}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.events = nil
}

func Events() []string {
	state.Lock()
	defer state.Unlock()
	return append([]string(nil), state.events...)
}

func record(event string) {
	state.Lock()
	defer state.Unlock()
	state.events = append(state.events, event)
}

func New(Options) (*Client, lifecycle.Cleanup, error) {
	record("construct client")
	return &Client{}, func(context.Context) error {
		record("cleanup client")
		return nil
	}, nil
}
`,
		"app/application.go": `package app

import "example.com/starterfixture/search"

type Server struct{}

// @Bean
func Options() search.Options {
	return search.Options{}
}

// @Bean
func NewServer(*search.Client) *Server {
	return &Server{}
}

// @Application
func Commerce(*Server) {
	panic("application marker must not execute during analysis")
}
`,
	})
}

const starterGeneratedRuntimeTest = `package spicegen

import (
	"context"
	"slices"
	"testing"

	"example.com/starterfixture/search"
)

func TestGeneratedStarterConstructionAndCleanup(t *testing.T) {
	search.Reset()
	application, err := NewApplication(context.Background())
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	if got, want := search.Events(), []string{"construct client"}; !slices.Equal(got, want) {
		t.Fatalf("construction events = %v, want %v", got, want)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got, want := search.Events(), []string{"construct client", "cleanup client"}; !slices.Equal(got, want) {
		t.Fatalf("stop events = %v, want %v", got, want)
	}
}
`
