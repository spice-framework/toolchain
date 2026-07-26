package provider

import (
	"strings"
	"testing"
)

func TestBuildEntrypointsValidatesExplicitStarterProviders(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/entrypoints\n\ngo 1.26.0\n",
		"search/search.go": `package search

type Options struct{}
type Client struct{}

func New(options Options) (*Client, error) {
	panic("starter entrypoint must not execute during analysis")
}

func Generic[T any]() T {
	panic("starter entrypoint must not execute during analysis")
}

func hidden() Client {
	panic("starter entrypoint must not execute during analysis")
}
`,
	})
	program, _ := loadAndResolve(t, root, "./...")

	catalog := BuildEntrypoints(program, []Entrypoint{{
		PackagePath:   "example.com/entrypoints/search",
		Symbol:        "New",
		SourceID:      "example.com/acme/starter/search",
		SourceVersion: "1.2.3",
	}})
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("BuildEntrypoints() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	providers := catalog.Providers()
	if len(providers) != 1 {
		t.Fatalf("Providers() = %#v", providers)
	}
	item := providers[0]
	if item.Source != SourceStarter ||
		item.SourceID != "example.com/acme/starter/search" ||
		item.SourceVersion != "1.2.3" ||
		item.OutputTypeID != "*example.com/entrypoints/search.Client" ||
		!item.ReturnsError ||
		item.ReturnsCleanup ||
		len(item.Dependencies) != 1 ||
		item.Dependencies[0].TypeID != "example.com/entrypoints/search.Options" {
		t.Fatalf("starter provider = %#v", item)
	}
}

func TestBuildEntrypointsRejectsInvalidSelectionsDeterministically(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/entrypoints\n\ngo 1.26.0\n",
		"search/search.go": `package search

type Client struct{}

func New() *Client { return &Client{} }
func Generic[T any]() T { var zero T; return zero }
func hidden() Client { return Client{} }
`,
	})
	program, _ := loadAndResolve(t, root, "./...")
	entrypoints := []Entrypoint{
		{
			PackagePath:   "example.com/entrypoints/search",
			Symbol:        "Missing",
			SourceID:      "example.com/acme/starter/search",
			SourceVersion: "1.0.0",
		},
		{
			PackagePath:   "example.com/entrypoints/search",
			Symbol:        "Generic",
			SourceID:      "example.com/acme/starter/search",
			SourceVersion: "1.0.0",
		},
		{
			PackagePath:   "example.com/entrypoints/search",
			Symbol:        "hidden",
			SourceID:      "example.com/acme/starter/search",
			SourceVersion: "1.0.0",
		},
		{
			PackagePath:   "example.com/entrypoints/search",
			Symbol:        "New",
			SourceID:      "",
			SourceVersion: "1.0.0",
		},
		{
			PackagePath:   "example.com/entrypoints/search",
			Symbol:        "New",
			SourceID:      "example.com/acme/starter/search",
			SourceVersion: "",
		},
		{
			PackagePath:   "example.com/entrypoints/search",
			Symbol:        "New",
			SourceID:      "example.com/acme/starter/search",
			SourceVersion: "1.0.0",
		},
		{
			PackagePath:   "example.com/entrypoints/search",
			Symbol:        "New",
			SourceID:      "example.com/acme/starter/search",
			SourceVersion: "1.0.0",
		},
	}

	catalog := BuildEntrypoints(program, entrypoints)
	joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
	for _, expected := range []string{
		"requires an exported Go symbol",
		"requires a trimmed source ID",
		"requires a trimmed source version",
		"is not a loaded package-level function",
		"generic provider functions are not supported",
		"is selected more than once",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
	if providers := catalog.Providers(); len(providers) != 1 ||
		providers[0].Name != "New" {
		t.Fatalf("Providers() = %#v", providers)
	}
}

func TestMergeRejectsBeanAndStarterProvidingSameExactType(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/entrypoints\n\ngo 1.26.0\n",
		"search/search.go": `package search

type Client struct{}

// @Bean
func New() *Client { return &Client{} }
`,
	})
	program, resolution := loadAndResolve(t, root, "./...")
	beanCatalog := Build(program, resolution)
	starterCatalog := BuildEntrypoints(program, []Entrypoint{{
		PackagePath:   "example.com/entrypoints/search",
		Symbol:        "New",
		SourceID:      "example.com/acme/starter/search",
		SourceVersion: "1.0.0",
	}})

	merged := Merge(beanCatalog, starterCatalog)
	joined := strings.Join(diagnosticStrings(merged.Diagnostics()), "\n")
	if !strings.Contains(joined, "multiple providers produce exact type") ||
		!strings.Contains(joined, "*example.com/entrypoints/search.Client") {
		t.Fatalf("Merge() diagnostics = %v", diagnosticStrings(merged.Diagnostics()))
	}
}

func TestBuildEntrypointsRejectsNilProgram(t *testing.T) {
	catalog := BuildEntrypoints(nil, nil)
	diagnostics := catalog.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Kind != "internal" ||
		!strings.Contains(diagnostics[0].Message, "loaded program") {
		t.Fatalf("Diagnostics() = %#v", diagnostics)
	}
}
