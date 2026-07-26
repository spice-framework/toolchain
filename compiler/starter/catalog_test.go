package starter_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/builtin"
	"github.com/StevenBuglione/spice/compiler/application"
	compilerbootstrap "github.com/StevenBuglione/spice/compiler/bootstrap"
	"github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
	compilerstarter "github.com/StevenBuglione/spice/compiler/starter"
	publicstarter "github.com/StevenBuglione/spice/starter"
)

const (
	currentGo = "go1.26.5"
	searchID  = "example.com/acme/starter/search"
)

func TestCatalogComposesDeterministicCompilerMetadata(t *testing.T) {
	annotated := annotatedManifest(t, searchID, "1.2.0", "search.Enable", "search.client", "search.transport")
	constructor := constructorManifest(t, "example.com/acme/starter/cache")
	catalog := newCatalog(t, annotated, constructor)
	manifests := catalog.Manifests()
	if got := []string{manifests[0].Spec().ID, manifests[1].Spec().ID}; !slices.Equal(
		got,
		[]string{"example.com/acme/starter/cache", "example.com/acme/starter/search"},
	) {
		t.Fatalf("manifest order = %v", got)
	}

	registry, err := catalog.Registry(builtin.Registry())
	if err != nil {
		t.Fatalf("Registry() error = %v", err)
	}
	if _, found := registry.Lookup("Application"); !found {
		t.Fatal("Registry() lost built-in @Application definition")
	}
	definition, found := registry.Lookup("search.Enable")
	if !found || len(definition.Arguments) != 1 {
		t.Fatalf("Registry() contributed definition = %#v, %t", definition, found)
	}

	definitions := catalog.BootstrapDefinitions()
	if len(definitions) != 1 {
		t.Fatalf("BootstrapDefinitions() count = %d", len(definitions))
	}
	got := definitions[0]
	if got.SourceID != searchID ||
		got.SourceVersion != "1.2.0" ||
		got.Annotation != "search.Enable" ||
		got.Capability != "search.client" {
		t.Fatalf("BootstrapDefinitions()[0] = %#v", got)
	}
	if len(got.Options) != 1 ||
		!slices.Equal(got.Options[0].AllowedStrings, []string{"orders", "products"}) ||
		!slices.Equal(
			got.Requirements,
			[]compilerbootstrap.RuntimeCapability{"search.transport"},
		) {
		t.Fatalf("BootstrapDefinitions()[0] semantics = %#v", got)
	}
	wantEntryPoints := []compilerbootstrap.EntryPoint{
		{Package: searchID, Symbol: "New"},
		{Package: searchID, Symbol: "NewAdmin"},
	}
	if !slices.Equal(got.EntryPoints, wantEntryPoints) ||
		!slices.Equal(catalog.EntryPoints("search.Enable"), wantEntryPoints) {
		t.Fatalf("entrypoints = %#v", got.EntryPoints)
	}

	definitions[0].Options[0].AllowedStrings[0] = "changed"
	definitions[0].EntryPoints[0].Symbol = "Changed"
	entryPoints := catalog.EntryPoints("search.Enable")
	if len(entryPoints) == 0 {
		t.Fatal("EntryPoints() returned no contributed entrypoints")
	}
	entryPoints[0].Symbol = "ChangedAgain"
	fresh := catalog.BootstrapDefinitions()[0]
	freshEntryPoints := catalog.EntryPoints("search.Enable")
	if len(freshEntryPoints) == 0 {
		t.Fatal("EntryPoints() lost contributed entrypoints")
	}
	if fresh.Options[0].AllowedStrings[0] == "changed" ||
		fresh.EntryPoints[0].Symbol != "New" ||
		freshEntryPoints[0].Symbol != "New" {
		t.Fatal("Catalog accessors returned mutable storage")
	}
}

func TestCatalogRejectsAmbiguousOrIncompatibleComposition(t *testing.T) {
	search := annotatedManifest(t, searchID, "1.2.0", "search.Enable", "search.client")
	searchAlias := annotatedManifest(t, "example.com/other/starter/search", "1.0.0", "search.Enable", "other.search")
	capabilityAlias := annotatedManifest(t, "example.com/other/starter/index", "1.0.0", "index.Enable", "search.client")
	builtinCapability := annotatedManifest(t, "example.com/other/starter/management", "1.0.0", "other.Management", "management")

	tests := []struct {
		name      string
		manifests []publicstarter.Manifest
		spiceAPI  string
		goVersion string
		want      string
	}{
		{name: "no manifests", want: "at least one manifest"},
		{name: "duplicate identity", manifests: []publicstarter.Manifest{search, search}, want: "duplicate starter manifest"},
		{name: "duplicate annotation", manifests: []publicstarter.Manifest{search, searchAlias}, want: "annotation @search.Enable is contributed by both"},
		{name: "duplicate capability", manifests: []publicstarter.Manifest{search, capabilityAlias}, want: `capability "search.client" is contributed by both`},
		{name: "built-in capability", manifests: []publicstarter.Manifest{builtinCapability}, want: `capability "management" is contributed by both`},
		{name: "Spice API", manifests: []publicstarter.Manifest{search}, spiceAPI: "v2alpha1", want: "requires Spice API"},
		{name: "Go version", manifests: []publicstarter.Manifest{search}, goVersion: "go1.25.9", want: "requires Go 1.26.0 or newer"},
		{name: "zero manifest", manifests: []publicstarter.Manifest{{}}, want: "manifest schema"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.spiceAPI == "" {
				test.spiceAPI = publicstarter.APIVersion
			}
			if test.goVersion == "" {
				test.goVersion = currentGo
			}
			_, err := compilerstarter.NewWithCompatibility(
				test.spiceAPI,
				test.goVersion,
				test.manifests...,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewWithCompatibility() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestCatalogRegistryRejectsBuiltInCollision(t *testing.T) {
	manifest := annotatedManifest(t, "example.com/acme/starter/management", "1.0.0", "management.Enable", "acme.management")
	catalog := newCatalog(t, manifest)
	_, err := catalog.Registry(builtin.Registry())
	if err == nil || !strings.Contains(err.Error(), `duplicate annotation definition "management.Enable"`) {
		t.Fatalf("Registry() error = %v", err)
	}
}

func TestBootstrapExtensionDefinitionsFailClosed(t *testing.T) {
	tests := []struct {
		name        string
		definitions []compilerbootstrap.Definition
		kind        string
		want        string
	}{
		{
			name: "partial source identity",
			definitions: []compilerbootstrap.Definition{
				{
					Annotation: "search.Enable",
					Capability: "search.client",
					SourceID:   "example.com/acme/starter/search",
				},
			},
			kind: "invalid-definition",
			want: "both source ID and source version",
		},
		{
			name: "unowned entrypoint",
			definitions: []compilerbootstrap.Definition{
				{
					Annotation: "search.Enable",
					Capability: "search.client",
					EntryPoints: []compilerbootstrap.EntryPoint{
						{Package: "example.com/acme/search", Symbol: "New"},
					},
				},
			},
			kind: "invalid-definition",
			want: "without source metadata",
		},
		{
			name: "duplicate capability",
			definitions: []compilerbootstrap.Definition{
				{Annotation: "search.Enable", Capability: "search.client"},
				{Annotation: "index.Enable", Capability: "search.client"},
			},
			kind: "duplicate-capability",
			want: `capability "search.client" is declared by both`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := compilerbootstrap.Compile(resolve.Result{}, nil, test.definitions)
			diagnostics := result.Diagnostics()
			if len(diagnostics) != 1 ||
				diagnostics[0].Kind != test.kind ||
				!strings.Contains(diagnostics[0].Message, test.want) {
				t.Fatalf("Compile() diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestCatalogFeaturesSurviveApplicationBuild(t *testing.T) {
	manifest := annotatedManifest(t, searchID, "1.2.0", "search.Enable", "search.client")
	catalog := newCatalog(t, manifest)
	if _, registryErr := catalog.Registry(builtin.Registry()); registryErr != nil {
		t.Fatalf("Registry() error = %v", registryErr)
	}

	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/catalogtest\n\ngo 1.26.0\n",
		"app/application.go": `package app

type Search struct{}

// @Bean
func NewSearch() Search {
	panic("provider bodies must not execute during analysis")
}

// @Application
// @search.Enable(indexes=["products", "orders"])
func Application(Search) {
	panic("application bodies must not execute during analysis")
}
`,
	})
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./app")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	model := buildModel(t, program, resolution, catalog)
	targets := model.Targets()
	features := targets[0].Bootstrap().Features()
	if len(features) != 1 {
		t.Fatalf("Features() count = %d", len(features))
	}
	feature := features[0]
	if feature.SourceID != searchID ||
		feature.SourceVersion != "1.2.0" ||
		!slices.Equal(
			feature.EntryPoints(),
			[]compilerbootstrap.EntryPoint{
				{Package: searchID, Symbol: "New"},
				{Package: searchID, Symbol: "NewAdmin"},
			},
		) {
		t.Fatalf("compiled Feature = %#v, entrypoints = %#v", feature, feature.EntryPoints())
	}
	indexes, found := feature.StringList("indexes")
	if !found || !slices.Equal(indexes, []string{"orders", "products"}) {
		t.Fatalf("indexes = %v, %t", indexes, found)
	}

	target, generationDiagnostics := generate.DefaultTarget(program, targets[0])
	if len(generationDiagnostics) != 0 {
		t.Fatalf("DefaultTarget() diagnostics = %v", generationDiagnostics)
	}
	firstPlan := renderPlan(t, program, model, target)
	repeatedPlan := renderPlan(t, program, model, target)
	if string(firstPlan.ManifestContent()) != string(repeatedPlan.ManifestContent()) {
		t.Fatal("identical starter metadata changed the ownership manifest")
	}

	versionSpec := manifest.Spec()
	versionSpec.Version = "1.2.1"
	versionModel := buildModel(t, program, resolution, newCatalog(t, mustManifest(t, versionSpec)))
	versionPlan := renderPlan(t, program, versionModel, target)
	if firstPlan.Manifest().InputSHA256 == versionPlan.Manifest().InputSHA256 {
		t.Fatal("starter version did not change the canonical model input hash")
	}

	entryPointSpec := manifest.Spec()
	entryPointSpec.Activation.EntryPoints[0].Symbol = "NewAlias"
	entryPointModel := buildModel(t, program, resolution, newCatalog(t, mustManifest(t, entryPointSpec)))
	entryPointPlan := renderPlan(t, program, entryPointModel, target)
	if firstPlan.Manifest().InputSHA256 == entryPointPlan.Manifest().InputSHA256 {
		t.Fatal("starter entrypoint did not change the canonical model input hash")
	}
}

func annotatedManifest(
	t *testing.T,
	id string,
	version string,
	annotationName string,
	capability string,
	requirements ...string,
) publicstarter.Manifest {
	t.Helper()
	return mustManifest(t, publicstarter.Spec{
		Schema:       publicstarter.Schema,
		ID:           id,
		Version:      version,
		Module:       strings.Split(id, "/starter/")[0],
		SpiceAPI:     publicstarter.APIVersion,
		MinimumGo:    "1.26.0",
		License:      "Apache-2.0",
		Review:       "docs/dependency-review.md",
		Capabilities: []string{capability},
		Activation: publicstarter.Activation{
			Mode: publicstarter.ActivationExplicitAnnotation,
			EntryPoints: []publicstarter.EntryPoint{
				{Package: id, Symbol: "NewAdmin"},
				{Package: id, Symbol: "New"},
			},
		},
		Annotations: []publicstarter.AnnotationSpec{
			{
				Name:    annotationName,
				Targets: []annotation.Target{annotation.TargetFunction},
				Arguments: []publicstarter.ArgumentSpec{
					{
						Name:             "indexes",
						Kinds:            []annotation.Kind{annotation.KindList},
						ListElementKinds: []annotation.Kind{annotation.KindString},
						Required:         true,
					},
				},
			},
		},
		ApplicationFeatures: []publicstarter.FeatureSpec{
			{
				Annotation: annotationName,
				Capability: capability,
				Options: []publicstarter.OptionSpec{
					{
						Name:           "indexes",
						Kind:           annotation.KindList,
						ListItemKinds:  []annotation.Kind{annotation.KindString},
						AllowedStrings: []string{"products", "orders"},
						Required:       true,
						UniqueItems:    true,
						MinimumItems:   1,
						SortItems:      true,
					},
				},
				Requirements: requirements,
			},
		},
	})
}

func constructorManifest(t *testing.T, id string) publicstarter.Manifest {
	t.Helper()
	return mustManifest(t, publicstarter.Spec{
		Schema:       publicstarter.Schema,
		ID:           id,
		Version:      "1.0.0",
		Module:       strings.Split(id, "/starter/")[0],
		SpiceAPI:     publicstarter.APIVersion,
		MinimumGo:    "1.26.0",
		License:      "Apache-2.0",
		Review:       "docs/dependency-review.md",
		Capabilities: []string{"cache.client"},
		Activation: publicstarter.Activation{
			Mode: publicstarter.ActivationExplicitConstructor,
			EntryPoints: []publicstarter.EntryPoint{
				{Package: id, Symbol: "New"},
			},
		},
	})
}

func mustManifest(t *testing.T, spec publicstarter.Spec) publicstarter.Manifest {
	t.Helper()
	manifest, err := publicstarter.New(spec)
	if err != nil {
		t.Fatalf("starter.New() error = %v", err)
	}
	return manifest
}

func newCatalog(t *testing.T, manifests ...publicstarter.Manifest) compilerstarter.Catalog {
	t.Helper()
	catalog, err := compilerstarter.NewWithCompatibility(
		publicstarter.APIVersion,
		currentGo,
		manifests...,
	)
	if err != nil {
		t.Fatalf("NewWithCompatibility() error = %v", err)
	}
	return catalog
}

func buildModel(
	t *testing.T,
	program *load.Program,
	resolution resolve.Result,
	catalog compilerstarter.Catalog,
) application.Model {
	t.Helper()
	model := application.BuildWithOptions(
		program,
		resolution,
		application.BuildOptions{
			BootstrapDefinitions: catalog.BootstrapDefinitions(),
		},
	)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("BuildWithOptions() diagnostics = %v", diagnostics)
	}
	if targets := model.Targets(); len(targets) != 1 {
		t.Fatalf("Targets() count = %d", len(targets))
	}
	return model
}

func renderPlan(
	t *testing.T,
	program *load.Program,
	model application.Model,
	target generate.Target,
) generate.Plan {
	t.Helper()
	targets := model.Targets()
	if len(targets) != 1 {
		t.Fatalf("Targets() count = %d", len(targets))
	}
	plan, diagnostics := generate.Render(program, model, targets[0], target)
	if len(diagnostics) != 0 {
		t.Fatalf("Render() diagnostics = %v", diagnostics)
	}
	return plan
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
