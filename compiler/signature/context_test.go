package signature

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/spice/compiler/load"
)

func TestContextTypeValidatesExactLoadedDeclaration(t *testing.T) {
	root := writeModule(t)
	program := loadProgram(t, root, nil)
	if ContextType(program) == nil {
		t.Fatal("ContextType() = nil for the canonical context package")
	}
	if ContextType(nil) != nil {
		t.Fatal("ContextType(nil) established an identity")
	}
}

func TestContextTypeRejectsNamedDoneElementAndAcceptsAlias(t *testing.T) {
	contextSource := filepath.Join(goRoot(t), "src", "context", "context.go")
	tests := []struct {
		name   string
		source string
		valid  bool
	}{
		{
			name: "named",
			source: `package context

import "time"

type doneToken struct{}

type Context interface {
	Deadline() (time.Time, bool)
	Done() <-chan doneToken
	Err() error
	Value(any) any
}
`,
		},
		{
			name: "alias",
			source: `package context

import "time"

type doneToken = struct{}

type Context interface {
	Deadline() (time.Time, bool)
	Done() <-chan doneToken
	Err() error
	Value(any) any
}
`,
			valid: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program := loadProgram(
				t,
				writeModule(t),
				map[string][]byte{contextSource: []byte(test.source)},
			)
			if got := ContextType(program) != nil; got != test.valid {
				t.Fatalf(
					"ContextType() identity established = %t, want %t",
					got,
					test.valid,
				)
			}
		})
	}
}

func loadProgram(
	t *testing.T,
	root string,
	overlay map[string][]byte,
) *load.Program {
	t.Helper()
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: root, Overlay: overlay},
		"./...",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	return program
}

func writeModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module example.com/signature\n\ngo 1.26.0\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	appDirectory := filepath.Join(root, "app")
	if err := os.MkdirAll(appDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(appDirectory, "app.go"),
		[]byte("package app\n\nimport \"context\"\n\nvar _ context.Context\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return root
}

func goRoot(t *testing.T) string {
	t.Helper()
	output, err := exec.CommandContext(
		t.Context(),
		"go",
		"env",
		"GOROOT",
	).Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	return strings.TrimSpace(string(output))
}
