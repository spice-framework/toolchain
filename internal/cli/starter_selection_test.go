package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
	compilerstarter "github.com/StevenBuglione/spice/compiler/starter"
	publicstarter "github.com/StevenBuglione/spice/starter"
)

const starterSelectionApplication = `// Package app is the selected application module.
//
// @Module
package app

import "example.com/cli-generation/searchstarter"

type Search struct {
	Client *searchstarter.Client
}

// @Bean
func SearchProvider(client *searchstarter.Client) Search {
	panic("provider bodies must not execute during analysis")
}

// @Application
// @search.Enable(indexes=["products"])
func Application(Search) {
	panic("application bodies must not execute during analysis")
}
`

func TestCommandsUseExplicitRepositoryStarterSelection(t *testing.T) {
	root := starterSelectionCLIModule(t, starterSelectionApplication)
	code, _, stderr := runModule(root, "verify", "./app")
	if code != 1 || !strings.Contains(stderr, "unknown annotation @search.Enable") {
		t.Fatalf("verify without selection: code=%d stderr=%q", code, stderr)
	}

	writeStarterSelection(t, root, "1.2.0", "New")
	code, stdout, stderr := runModule(root, "verify", "./app")
	if code != 0 || !strings.Contains(stdout, "verification passed") || stderr != "" {
		t.Fatalf("verify: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runModule(root, "modules", "--format=json", "./app")
	if code != 0 ||
		!strings.Contains(stdout, "example.com/cli-generation/app") ||
		strings.Contains(stdout, "searchstarter") ||
		stderr != "" {
		t.Fatalf("modules: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runModule(
		root,
		"test",
		"--module=example.com/cli-generation/app",
		"./app",
	)
	if code != 0 || !strings.Contains(stdout, "module tests passed") || stderr != "" {
		t.Fatalf("test: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runModule(root, "generate", "./app")
	if code != 0 || !strings.Contains(stdout, "generated target Application") || stderr != "" {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	generatedPath := filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"zz_spice_gen.go",
	)
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "searchstarter.New()") ||
		!strings.Contains(string(generated), `searchstarter "example.com/cli-generation/searchstarter"`) {
		t.Fatalf("selection did not emit the starter constructor:\n%s", generated)
	}
	code, stdout, stderr = runModule(root, "build", "./app")
	if code != 0 || !strings.Contains(stdout, "build passed") || stderr != "" {
		t.Fatalf("build: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	writeStarterSelection(t, root, "1.2.1", "New")
	code, stdout, stderr = runModule(root, "generate", "--check", "./app")
	if code != 1 ||
		stdout != "" ||
		!strings.Contains(stderr, "generation is stale") {
		t.Fatalf("changed selection: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestExplicitAnnotationStarterDoesNotActivateWithoutAnnotation(t *testing.T) {
	root := starterSelectionCLIModule(t, generationApplicationSource)
	writeStarterSelection(t, root, "1.2.0", "New")

	code, stdout, stderr := runModule(root, "generate", "./app")
	if code != 0 || !strings.Contains(stdout, "generated target Application") || stderr != "" {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	generated, err := os.ReadFile(filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"zz_spice_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(generated), "searchstarter.New") {
		t.Fatalf("absent annotation activated starter constructor:\n%s", generated)
	}
}

func TestExplicitConstructorStarterGeneratesProvider(t *testing.T) {
	root := starterSelectionCLIModule(t, `package app

import "example.com/cli-generation/searchstarter"

type ApplicationService struct {
	Client *searchstarter.Client
}

// @Bean
func Service(client *searchstarter.Client) *ApplicationService {
	return &ApplicationService{Client: client}
}

// @Application
func Application(*ApplicationService) {}
`)
	writeConstructorStarterSelection(t, root)

	code, stdout, stderr := runModule(root, "generate", "./app")
	if code != 0 || !strings.Contains(stdout, "generated target Application") || stderr != "" {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	generated, err := os.ReadFile(filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"zz_spice_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "searchstarter.New()") {
		t.Fatalf("explicit constructor did not generate provider call:\n%s", generated)
	}
}

func TestCommandsRejectMissingSelectedStarterEntrypoint(t *testing.T) {
	root := starterSelectionCLIModule(t, starterSelectionApplication)
	writeStarterSelection(t, root, "1.2.0", "Missing")

	code, stdout, stderr := runModule(root, "verify", "./app")
	if code != 1 ||
		stdout != "" ||
		!strings.Contains(stderr, "starter entrypoint error") ||
		!strings.Contains(stderr, "searchstarter.Missing") ||
		!strings.Contains(stderr, "is not a loaded package-level function") {
		t.Fatalf("verify: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCommandsRejectInvalidStarterSelection(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)
	path := filepath.Join(root, filepath.FromSlash(starterSelectionPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(`{"schema":"spice.starters/v1","manifests":[],"unknown":true}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runModule(root, "generate", "./...")
	if code != 1 ||
		stdout != "" ||
		!strings.Contains(stderr, "unknown field") ||
		!strings.Contains(stderr, starterSelectionPath) ||
		strings.Contains(stderr, root) {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "spicegen")); !os.IsNotExist(err) {
		t.Fatalf("invalid selection wrote generated output: %v", err)
	}
}

func TestCompilerMetadataRejectsUnsafeSelectionFiles(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, filepath.FromSlash(starterSelectionPath))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := loadCompilerMetadata(load.Options{Dir: root})
		if err == nil || !strings.Contains(err.Error(), "must be a regular file") {
			t.Fatalf("loadCompilerMetadata() error = %v", err)
		}
	})

	t.Run("size limit", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, filepath.FromSlash(starterSelectionPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		file, createErr := os.Create(path)
		if createErr != nil {
			t.Fatal(createErr)
		}
		truncateErr := file.Truncate(maxStarterSelectionSize + 1)
		closeErr := file.Close()
		if truncateErr != nil {
			t.Fatal(truncateErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		_, err := loadCompilerMetadata(load.Options{Dir: root})
		if err == nil || !strings.Contains(err.Error(), "exceeds the") {
			t.Fatalf("loadCompilerMetadata() error = %v", err)
		}
	})
}

func writeStarterSelection(
	t *testing.T,
	root string,
	version string,
	entryPoint string,
) {
	t.Helper()
	const starterID = "example.com/acme/starter/search"
	const entryPointPackage = "example.com/cli-generation/searchstarter"
	manifest, err := publicstarter.New(publicstarter.Spec{
		Schema:       publicstarter.Schema,
		ID:           starterID,
		Version:      version,
		Module:       "example.com/acme",
		SpiceAPI:     publicstarter.APIVersion,
		MinimumGo:    "1.26.0",
		License:      "Apache-2.0",
		Review:       "docs/dependency-review.md",
		Capabilities: []string{"search.client"},
		Activation: publicstarter.Activation{
			Mode: publicstarter.ActivationExplicitAnnotation,
			EntryPoints: []publicstarter.EntryPoint{
				{Package: entryPointPackage, Symbol: entryPoint},
			},
		},
		Annotations: []publicstarter.AnnotationSpec{
			{
				Name:    "search.Enable",
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
				Annotation: "search.Enable",
				Capability: "search.client",
				EntryPoints: []publicstarter.EntryPoint{
					{Package: entryPointPackage, Symbol: entryPoint},
				},
				Options: []publicstarter.OptionSpec{
					{
						Name:          "indexes",
						Kind:          annotation.KindList,
						ListItemKinds: []annotation.Kind{annotation.KindString},
						Required:      true,
						UniqueItems:   true,
						MinimumItems:  1,
						SortItems:     true,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("starter.New() error = %v", err)
	}
	writeStarterCatalog(t, root, manifest)
}

func writeConstructorStarterSelection(t *testing.T, root string) {
	t.Helper()
	manifest, err := publicstarter.New(publicstarter.Spec{
		Schema:       publicstarter.Schema,
		ID:           "example.com/acme/starter/search",
		Version:      "1.2.0",
		Module:       "example.com/acme",
		SpiceAPI:     publicstarter.APIVersion,
		MinimumGo:    "1.26.0",
		License:      "Apache-2.0",
		Review:       "docs/dependency-review.md",
		Capabilities: []string{"search.client"},
		Activation: publicstarter.Activation{
			Mode: publicstarter.ActivationExplicitConstructor,
			EntryPoints: []publicstarter.EntryPoint{{
				Package: "example.com/cli-generation/searchstarter",
				Symbol:  "New",
			}},
		},
	})
	if err != nil {
		t.Fatalf("starter.New() error = %v", err)
	}
	writeStarterCatalog(t, root, manifest)
}

func writeStarterCatalog(t *testing.T, root string, manifest publicstarter.Manifest) {
	t.Helper()
	catalog, err := compilerstarter.NewWithCompatibility(
		publicstarter.APIVersion,
		"go1.26.5",
		manifest,
	)
	if err != nil {
		t.Fatalf("compilerstarter.NewWithCompatibility() error = %v", err)
	}
	content, err := catalog.JSON()
	if err != nil {
		t.Fatalf("Catalog.JSON() error = %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(starterSelectionPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func starterSelectionCLIModule(t *testing.T, source string) string {
	t.Helper()
	root := generationCLIModule(t, source)
	path := filepath.Join(root, "searchstarter", "search.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`// Package searchstarter is explicit compiler support.
//
// @Module
package searchstarter

type Client struct{}

// @Bean
func New() *Client {
	panic("starter entrypoints must not execute during analysis")
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
