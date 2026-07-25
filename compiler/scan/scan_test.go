package scan

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestTreeScansPackageAndValueDeclarations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `// Package sample exercises package and value annotations.
//
// @Configuration
package sample

// @Bean
const Constant = 1

var (
	// @Service
	Value int
)

func Undocumented() {}
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "vendor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "ignored.go"), []byte("package ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Tree(root)
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if result.Files != 1 {
		t.Fatalf("Files = %d, want 1", result.Files)
	}
	if len(result.Occurrences) != 3 {
		t.Fatalf("Occurrences = %#v, want package, constant, and variable", result.Occurrences)
	}
	if result.Occurrences[0].Target != TargetPackage {
		t.Fatalf("package occurrence = %#v", result.Occurrences[0])
	}
	if result.Occurrences[1].Target != TargetConstant {
		t.Fatalf("constant occurrence = %#v", result.Occurrences[1])
	}
	if result.Occurrences[2].Target != TargetVariable {
		t.Fatalf("variable occurrence = %#v", result.Occurrences[2])
	}
}

func TestTreeIsDeterministicAcrossRepeatedScans(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `package sample

// @Controller(prefix="/users")
type Controller struct{}

// @Get(path="/{id}")
func (Controller) Get() {}
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	first, err := Tree(root)
	if err != nil {
		t.Fatalf("first Tree() error = %v", err)
	}
	for run := range 5 {
		next, err := Tree(root)
		if err != nil {
			t.Fatalf("run %d Tree() error = %v", run, err)
		}
		if !reflect.DeepEqual(next, first) {
			t.Fatalf("run %d result = %#v, want %#v", run, next, first)
		}
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
