package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/modulith"
)

func TestRunVerifyEnforcesModuleImportBoundaries(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
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
// @Module(allowedDependencies=["example.com/fixture/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import "example.com/fixture/inventory/storage"

var Store storage.Store
`,
	})
	code, stdout, stderr := runModule(root, "verify", "./...")
	if code != 1 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{
		"imports internal package example.com/fixture/inventory/storage",
		"module architecture error(s)",
	} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
	code, stdout, stderr = runModule(root, "modules", "--format=json", "./...")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "module architecture error(s)") {
		t.Fatalf("modules code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunVerifyAcceptsDeclaredModuleRootAPI(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory

type Store struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/fixture/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import "example.com/fixture/inventory"

var Store inventory.Store
`,
	})
	code, stdout, stderr := runModule(root, "verify", "./...")
	if code != 0 || !strings.Contains(stdout, "verification passed") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunModulesRendersEverySupportedFormat(t *testing.T) {
	t.Parallel()
	root := moduleDocumentationFixture(t)
	tests := []struct {
		name      string
		arguments []string
		expected  []string
	}{
		{
			name:      "json-default",
			arguments: []string{"modules", "./..."},
			expected: []string{
				`"schema": "spice.modules/v1"`,
				`"id": "example.com/fixture/orders"`,
				`"observed_dependencies": [`,
			},
		},
		{
			name:      "mermaid",
			arguments: []string{"modules", "--format=mermaid", "./..."},
			expected:  []string{"flowchart LR", "M1 -->|default| M0"},
		},
		{
			name:      "plantuml",
			arguments: []string{"modules", "--format", "plantuml", "./..."},
			expected:  []string{"@startuml", "M1 --> M0 : default", "@enduml"},
		},
		{
			name: "focused-json",
			arguments: []string{
				"modules",
				"--focus=example.com/fixture/orders",
				"--format=json",
				"./...",
			},
			expected: []string{
				`"focus": "example.com/fixture/orders"`,
				`"dependency_order": [`,
				`"example.com/fixture/inventory"`,
				`"example.com/fixture/orders"`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runModule(root, test.arguments...)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			for _, expected := range test.expected {
				if !strings.Contains(stdout, expected) {
					t.Fatalf("stdout=%q missing %q", stdout, expected)
				}
			}
		})
	}
}

func TestParseModuleArgumentsRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		{"--format"},
		{"--format="},
		{"--format=dot"},
		{"--unknown"},
		{"--format=json", "--format", "mermaid"},
		{"--focus"},
		{"--focus="},
		{"--focus=example.com/one", "--focus", "example.com/two"},
	}
	for _, arguments := range tests {
		if _, err := parseModuleArguments(arguments); err == nil {
			t.Fatalf("parseModuleArguments(%v) error = nil", arguments)
		}
	}
	parsed, err := parseModuleArguments([]string{"--format", "mermaid", "./orders", "./inventory"})
	if err != nil {
		t.Fatalf("parseModuleArguments(valid) error = %v", err)
	}
	if parsed.format != modulith.FormatMermaid ||
		parsed.focus != "" ||
		!slices.Equal(parsed.patterns, []string{"./orders", "./inventory"}) {
		t.Fatalf("parseModuleArguments(valid) = %#v", parsed)
	}

	root := moduleDocumentationFixture(t)
	code, stdout, stderr := runModule(
		root,
		"modules",
		"--focus=example.com/fixture/missing",
		"./...",
	)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "focus module") {
		t.Fatalf("unknown focus code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func moduleDocumentationFixture(t *testing.T) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory

type Store struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/fixture/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import "example.com/fixture/inventory"

var Store inventory.Store
`,
	})
}
