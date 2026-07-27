package descriptor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
)

func TestDecodeStaticDescriptor(t *testing.T) {
	program := loadDescriptorProgram(t, map[string]string{
		"defs/controller.go": `package defs

import "github.com/StevenBuglione/spice/annotation/sdk"

// Controller marks an HTTP controller and documents its generated behavior.
func Controller() sdk.Definition {
	return sdk.Definition{
		Name: "web.Controller",
		Summary: "Marks an HTTP controller.",
		Targets: []sdk.Target{sdk.TargetType},
		Arguments: []sdk.Argument{{
			Name: "prefix",
			Kinds: []sdk.Kind{sdk.KindString},
			Description: "Optional route prefix.",
		}},
		Examples: []sdk.Example{{
			Title: "Controller",
			Code: "// @Controller",
		}},
		Compatibility: sdk.Compatibility{
			Since: "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool: "example.com/plugin/cmd/annotations",
			Handler: "web/controller",
			Protocol: sdk.ProtocolV1Alpha1,
			Source: sdk.Symbol{
				Package: "example.com/plugin/internal/web",
				Name: "ControllerHandler",
			},
		},
	}
}
`,
	})

	got, err := Decode(program, "example.com/plugin/defs", "Controller")
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Definition.Name != "web.Controller" ||
		got.Definition.Arguments[0].Name != "prefix" ||
		got.Definition.Implementation.Handler != "web/controller" ||
		!strings.Contains(got.Documentation, "generated behavior") ||
		got.Package != "example.com/plugin/defs" ||
		got.Symbol != "Controller" ||
		got.Position.Filename == "" {
		t.Fatalf("Decode() = %#v", got)
	}
}

func TestDecodeAllOfficialDescriptors(t *testing.T) {
	t.Parallel()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository) error = %v", err)
	}
	references := []annotation.DefinitionReference{
		{Package: "github.com/StevenBuglione/spice/annotation/async", Symbol: "Execute"},
		{Package: "github.com/StevenBuglione/spice/annotation/cache", Symbol: "Cacheable"},
		{Package: "github.com/StevenBuglione/spice/annotation/core", Symbol: "Application"},
		{Package: "github.com/StevenBuglione/spice/annotation/core", Symbol: "Bean"},
		{Package: "github.com/StevenBuglione/spice/annotation/core", Symbol: "Configuration"},
		{Package: "github.com/StevenBuglione/spice/annotation/core", Symbol: "Service"},
		{Package: "github.com/StevenBuglione/spice/annotation/data", Symbol: "Transactional"},
		{Package: "github.com/StevenBuglione/spice/annotation/event", Symbol: "Listener"},
		{Package: "github.com/StevenBuglione/spice/annotation/event", Symbol: "Topic"},
		{Package: "github.com/StevenBuglione/spice/annotation/lifecycle", Symbol: "OnStart"},
		{Package: "github.com/StevenBuglione/spice/annotation/lifecycle", Symbol: "OnStop"},
		{Package: "github.com/StevenBuglione/spice/annotation/management", Symbol: "Enable"},
		{Package: "github.com/StevenBuglione/spice/annotation/modulith", Symbol: "Module"},
		{Package: "github.com/StevenBuglione/spice/annotation/modulith", Symbol: "NamedInterface"},
		{Package: "github.com/StevenBuglione/spice/annotation/observability", Symbol: "Logging"},
		{Package: "github.com/StevenBuglione/spice/annotation/schedule", Symbol: "FixedDelay"},
		{Package: "github.com/StevenBuglione/spice/annotation/security", Symbol: "Authorize"},
		{Package: "github.com/StevenBuglione/spice/annotation/web", Symbol: "Controller"},
		{Package: "github.com/StevenBuglione/spice/annotation/web", Symbol: "Get"},
		{Package: "github.com/StevenBuglione/spice/annotation/web", Symbol: "Post"},
	}
	packages := make([]string, 0, len(references))
	for _, reference := range references {
		packages = append(packages, reference.Package)
	}
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: repository},
		packages...,
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	descriptors, err := DecodeAll(program, references)
	if err != nil {
		t.Fatalf("DecodeAll() error = %v", err)
	}
	if len(descriptors) != len(references) {
		t.Fatalf(
			"DecodeAll() descriptors = %d, want %d",
			len(descriptors),
			len(references),
		)
	}
	for _, item := range descriptors {
		if item.Documentation == "" ||
			item.Definition.Implementation.Tool !=
				"github.com/StevenBuglione/spice/cmd/spice-annotation-core" ||
			item.Definition.Implementation.Source.Package !=
				"github.com/StevenBuglione/spice/internal/annotationcore" {
			t.Fatalf("official descriptor = %+v", item)
		}
	}
}

func TestDecodeRejectsExecutableOrAmbiguousDescriptors(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		symbol  string
		message string
	}{
		{
			name: "computed body",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
func summary() string { return "computed" }
// Controller documents the descriptor.
func Controller() sdk.Definition {
	return sdk.Definition{Name: "Controller", Summary: summary()}
}
`,
			symbol:  "Controller",
			message: "string literal or exported SDK string constant",
		},
		{
			name: "local constant",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
const descriptorName = "Controller"
// Controller documents the descriptor.
func Controller() sdk.Definition {
	return sdk.Definition{Name: descriptorName}
}
`,
			symbol:  "Controller",
			message: "string literal or exported SDK string constant",
		},
		{
			name: "computed Boolean constant",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
const repeatable = true
// Controller documents the descriptor.
func Controller() sdk.Definition {
	return sdk.Definition{Name: "Controller", Repeatable: repeatable}
}
`,
			symbol:  "Controller",
			message: "Boolean literal",
		},
		{
			name: "control flow",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
// Controller documents the descriptor.
func Controller() sdk.Definition {
	if true { return sdk.Definition{} }
	return sdk.Definition{}
}
`,
			symbol:  "Controller",
			message: "body must contain only",
		},
		{
			name: "wrong suffix",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
// Controller documents the descriptor.
func Controller() sdk.Definition {
	return sdk.Definition{Name: "web.Route"}
}
`,
			symbol:  "Controller",
			message: `must end in descriptor symbol "Controller"`,
		},
		{
			name: "multiple descriptors",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
// Controller documents the descriptor.
func Controller() sdk.Definition { return sdk.Definition{} }
// Get documents the descriptor.
func Get() sdk.Definition { return sdk.Definition{} }
`,
			symbol:  "Controller",
			message: "only exported annotation descriptor",
		},
		{
			name: "missing docs",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
func Controller() sdk.Definition { return sdk.Definition{} }
`,
			symbol:  "Controller",
			message: "requires a GoDoc comment",
		},
		{
			name: "wrong signature",
			source: `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
// Controller documents the descriptor.
func Controller(value string) sdk.Definition { return sdk.Definition{} }
`,
			symbol:  "Controller",
			message: "exact signature",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program := loadDescriptorProgram(t, map[string]string{
				"defs/definition.go": test.source,
			})
			_, err := Decode(program, "example.com/plugin/defs", test.symbol)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Decode() error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestDecodeRejectsMissingProgramPackageAndSymbol(t *testing.T) {
	if _, err := Decode(nil, "example.com/plugin/defs", "Controller"); err == nil {
		t.Fatal("Decode(nil) error = nil")
	}
	program := loadDescriptorProgram(t, map[string]string{
		"defs/definition.go": "package defs\n",
	})
	for _, test := range []struct {
		pkg, symbol, message string
	}{
		{"example.com/missing", "Controller", "package is not in"},
		{"example.com/plugin/defs", "Controller", "was not found"},
	} {
		_, err := Decode(program, test.pkg, test.symbol)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("Decode(%q, %q) error = %v", test.pkg, test.symbol, err)
		}
	}
}

func TestDecodeAllBuildsGenericDefinitionsAndRejectsCanonicalCollisions(
	t *testing.T,
) {
	program := loadDescriptorProgram(t, map[string]string{
		"defs/controller.go": validDescriptorSource(
			"Controller",
			"web.Controller",
		),
		"defs/duplicate.go": validDescriptorSource(
			"Duplicate",
			"web.Duplicate",
		),
		"defs2/controller.go": validDescriptorSource(
			"Controller",
			"web.Controller",
		),
	})
	references := []annotation.DefinitionReference{{
		Package: "example.com/plugin/defs",
		Symbol:  "Controller",
	}}
	descriptors, err := DecodeAll(program, references)
	if err != nil {
		t.Fatalf("DecodeAll() error = %v", err)
	}
	definition, err := descriptors[0].RegistryDefinition()
	if err != nil {
		t.Fatalf("RegistryDefinition() error = %v", err)
	}
	if definition.Name != "web.Controller" ||
		!definition.Targets.Contains(annotation.TargetType) {
		t.Fatalf("definition = %#v", definition)
	}
	_, err = DecodeAll(program, append(references, annotation.DefinitionReference{
		Package: "example.com/plugin/defs2",
		Symbol:  "Controller",
	}))
	if err == nil || !strings.Contains(err.Error(), "both define canonical name") {
		t.Fatalf("DecodeAll() collision error = %v", err)
	}
}

func validDescriptorSource(symbol, name string) string {
	return `package defs
import "github.com/StevenBuglione/spice/annotation/sdk"
// ` + symbol + ` documents the annotation.
func ` + symbol + `() sdk.Definition {
	return sdk.Definition{
		Name: "` + name + `",
		Summary: "Test annotation.",
		Targets: []sdk.Target{sdk.TargetType},
		Examples: []sdk.Example{{Title: "Use", Code: "// @` + symbol + `"}},
		Compatibility: sdk.Compatibility{
			Since: "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool: "example.com/plugin/cmd/annotations",
			Handler: "web/` + strings.ToLower(symbol) + `",
			Protocol: sdk.ProtocolV1Alpha1,
			Source: sdk.Symbol{
				Package: "example.com/plugin/internal/web",
				Name: "` + symbol + `Handler",
			},
		},
	}
}
`
}

func loadDescriptorProgram(
	t *testing.T,
	files map[string]string,
) *load.Program {
	t.Helper()
	root := t.TempDir()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	module := "module example.com/plugin\n\ngo 1.26.0\n\n" +
		"require github.com/StevenBuglione/spice v0.0.0\n\n" +
		"replace github.com/StevenBuglione/spice => " +
		filepath.ToSlash(repositoryRoot) + "\n"
	writeDescriptorFile(t, root, "go.mod", module)
	writeDescriptorFile(t, root, "app/app.go", "package app\n")
	auxiliary := make(map[string]struct{})
	for name, content := range files {
		writeDescriptorFile(t, root, name, content)
		directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(name)))
		if directory != "." && directory != "app" {
			auxiliary["example.com/plugin/"+directory] = struct{}{}
		}
	}
	auxiliaryPackages := make([]string, 0, len(auxiliary))
	for packagePath := range auxiliary {
		auxiliaryPackages = append(auxiliaryPackages, packagePath)
	}
	program, err := load.Load(
		context.Background(),
		load.Options{
			Dir:               root,
			AuxiliaryPackages: auxiliaryPackages,
		},
		"./app",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	return program
}

func writeDescriptorFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
