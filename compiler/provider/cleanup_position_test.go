package provider

import (
	"fmt"
	"strings"
	"testing"
)

func TestCatalogTreatsFirstCleanupResultAsProvidedOutput(t *testing.T) {
	tests := []struct {
		name         string
		declarations string
		signature    string
	}{
		{
			name:      "canonical",
			signature: "(life.Cleanup, life.Cleanup, error)",
		},
		{
			name: "aliases",
			declarations: `type OutputAlias = life.Cleanup
	type MetadataAlias = life.Cleanup
	type ErrorAlias = error`,
			signature: "(OutputAlias, MetadataAlias, ErrorAlias)",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeModule(t, map[string]string{
				"go.mod": "module github.com/StevenBuglione/spice\n\ngo 1.23.0\n",
				"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
				"app/providers.go": fmt.Sprintf(`package app

import life "github.com/StevenBuglione/spice/lifecycle"

%s

// @Bean
func Provider() %s { panic("provider and cleanup bodies must not execute") }
`, test.declarations, test.signature),
			})

			program, resolved := loadAndResolve(t, root, "./app")
			catalog := buildQuiet(t, program, resolved)
			if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
				t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
			}
			providers := catalog.Providers()
			if len(providers) != 1 {
				t.Fatalf("Providers() = %#v, want one provider", providers)
			}
			item := providers[0]
			if !item.ReturnsCleanup || !item.ReturnsError {
				t.Fatalf("flags cleanup=%v error=%v, want both true", item.ReturnsCleanup, item.ReturnsError)
			}
			if !isCleanup(item.Output, canonicalCleanupType(program)) {
				t.Fatalf("first result was not retained as cleanup-typed output: %s", item.OutputTypeID)
			}
		})
	}
}

func TestCatalogRejectsWrongShapeCanonicalCleanupReplacement(t *testing.T) {
	tests := []struct {
		name        string
		declaration string
	}{
		{name: "missing-context", declaration: "package lifecycle\n\ntype Cleanup func() error\n"},
		{name: "missing-error", declaration: "package lifecycle\n\nimport \"context\"\n\ntype Cleanup func(context.Context)\n"},
		{name: "not-function", declaration: "package lifecycle\n\ntype Cleanup string\n"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeModule(t, map[string]string{
				"go.mod": `module example.com/application


go 1.23.0

require github.com/StevenBuglione/spice v0.0.0
replace github.com/StevenBuglione/spice => ./fake-spice
`,
				"fake-spice/go.mod": `module github.com/StevenBuglione/spice


go 1.23.0
`,
				"fake-spice/lifecycle/cleanup.go": test.declaration,
				"app/providers.go": `package app

import life "github.com/StevenBuglione/spice/lifecycle"

type Value struct{}

// @Bean
func Provider() (Value, life.Cleanup) { panic("provider and cleanup bodies must not execute") }
`,
			})

			program, resolved := loadAndResolve(t, root, "./app")
			catalog := buildQuiet(t, program, resolved)
			if len(catalog.Providers()) != 0 {
				t.Fatalf("wrong-shape cleanup provider entered catalog: %#v", catalog.Providers())
			}
			diagnostics := catalog.Diagnostics()
			if len(diagnostics) != 1 {
				t.Fatalf("Diagnostics() = %v, want one diagnostic", diagnosticStrings(diagnostics))
			}
			message := diagnostics[0].Error()
			for _, expected := range []string{"Provider", "second result must be lifecycle.Cleanup or error", "accepted forms are"} {
				if !strings.Contains(message, expected) {
					t.Fatalf("diagnostic %q missing %q", message, expected)
				}
			}
		})
	}
}
