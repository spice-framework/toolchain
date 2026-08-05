package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/spice/compiler/load"
	"github.com/spice-framework/spice/compiler/modulith"
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

func TestRunModuleTestSelectsFocusedDependencyPackages(t *testing.T) {
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
		"payments/package.go": `// Package payments is unrelated to the focused graph.
//
// @Module
package payments
`,
		"unassigned/helper.go": `package unassigned
`,
	})
	var directory string
	var arguments []string
	tester := func(
		ctx context.Context,
		gotDirectory string,
		gotArguments []string,
		stdout io.Writer,
		_ io.Writer,
	) error {
		if ctx == nil {
			t.Fatal("test context is nil")
		}
		directory = gotDirectory
		arguments = append([]string(nil), gotArguments...)
		if _, err := io.WriteString(stdout, "ordinary go test output\n"); err != nil {
			return err
		}
		return nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithExecutors(
		[]string{
			"test",
			"--module=example.com/fixture/orders",
			"--race",
			"--count=2",
			"--run=Order",
			"--timeout=3s",
			"./...",
		},
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
		executeGoBuild,
		tester,
	)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if directory != root {
		t.Fatalf("test directory = %q, want %q", directory, root)
	}
	wantArguments := []string{
		"-trimpath",
		"-race",
		"-count=2",
		"-run=Order",
		"-timeout=3s",
		"example.com/fixture/inventory",
		"example.com/fixture/orders",
		"example.com/fixture/orders/use",
	}
	if !slices.Equal(arguments, wantArguments) {
		t.Fatalf("test arguments = %#v, want %#v", arguments, wantArguments)
	}
	for _, expected := range []string{
		"ordinary go test output",
		"Spice module tests passed for example.com/fixture/orders: 3 package(s) across 2 module(s).",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout=%q missing %q", stdout.String(), expected)
		}
	}
}

func TestRunModuleTestReportsExecutorFailure(t *testing.T) {
	t.Parallel()
	root := moduleDocumentationFixture(t)
	sentinel := errors.New("test executor failed")
	tester := func(context.Context, string, []string, io.Writer, io.Writer) error {
		return sentinel
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithExecutors(
		[]string{"test", "--module=example.com/fixture/orders", "./..."},
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
		executeGoBuild,
		tester,
	)
	if code != 1 ||
		stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), sentinel.Error()) ||
		!strings.Contains(stderr.String(), "Spice module test failed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunModuleTestRejectsArchitectureBeforeExecution(t *testing.T) {
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
	called := false
	tester := func(context.Context, string, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithExecutors(
		[]string{"test", "--module=example.com/fixture/orders", "./..."},
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
		executeGoBuild,
		tester,
	)
	if code != 1 ||
		called ||
		stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "imports internal package") ||
		!strings.Contains(stderr.String(), "module architecture error(s)") {
		t.Fatalf(
			"code=%d called=%t stdout=%q stderr=%q",
			code,
			called,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestParseModuleTestArgumentsRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		nil,
		{"--module"},
		{"--module="},
		{"--module=one", "--module=two"},
		{"--race", "--race", "--module=one"},
		{"--count=0", "--module=one"},
		{"--count=no", "--module=one"},
		{"--run=", "--module=one"},
		{"--timeout=0s", "--module=one"},
		{"--timeout=forever", "--module=one"},
		{"--unknown", "--module=one"},
	}
	for _, arguments := range tests {
		if _, err := parseModuleTestArguments(arguments); err == nil {
			t.Fatalf("parseModuleTestArguments(%v) error = nil", arguments)
		}
	}
	parsed, err := parseModuleTestArguments([]string{
		"--module",
		"example.com/fixture/orders",
		"./orders/...",
	})
	if err != nil {
		t.Fatalf("parseModuleTestArguments(valid) error = %v", err)
	}
	if parsed.module != "example.com/fixture/orders" ||
		!slices.Equal(parsed.patterns, []string{"./orders/..."}) {
		t.Fatalf("parseModuleTestArguments(valid) = %#v", parsed)
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
