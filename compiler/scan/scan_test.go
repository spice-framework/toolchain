package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreeAssociatesAnnotationsWithDeclarations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `package sample

// @Controller(prefix="/users")
type Controller struct{}

// @Get(path="/{id}")
func (Controller) Get() {}

// @Service
func NewService() {}
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := Tree(root)
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if result.Files != 1 {
		t.Fatalf("Files = %d, want 1", result.Files)
	}
	if len(result.Occurrences) != 3 {
		t.Fatalf("len(Occurrences) = %d, want 3", len(result.Occurrences))
	}

	if result.Occurrences[0].Target != TargetType || result.Occurrences[0].Name != "Controller" {
		t.Fatalf("first occurrence = %#v", result.Occurrences[0])
	}
	if result.Occurrences[1].Target != TargetMethod || result.Occurrences[1].Name != "Get" {
		t.Fatalf("second occurrence = %#v", result.Occurrences[1])
	}
	if result.Occurrences[2].Target != TargetFunction || result.Occurrences[2].Name != "NewService" {
		t.Fatalf("third occurrence = %#v", result.Occurrences[2])
	}
}

func TestTreeRejectsMalformedAnnotation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `package sample

// @Controller(prefix=)
type Controller struct{}
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Tree(root); err == nil {
		t.Fatal("Tree() did not reject malformed annotation")
	}
}

func TestPathRoot(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"":                 ".",
		"...":              ".",
		"./...":            ".",
		"./examples/...":   "./examples",
		"./examples/app":   "./examples/app",
		"examples/app/...": "examples/app",
	}
	for input, want := range tests {
		if got := PathRoot(input); got != want {
			t.Fatalf("PathRoot(%q) = %q, want %q", input, got, want)
		}
	}
}
